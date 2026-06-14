"""Pure, dependency-free core for the SlipSpace Arbiter reference detectors.

This module holds everything that can be unit-tested without torch/transformers:
the slipspace.detect.v1 protojson contract (parse request / build response), the
sliding-window chunk planner, and the cross-chunk finding reduce. app.py wires a
real model around it.

Wire shape: Go emits protojson (camelCase) and reads it back with
DiscardUnknown, accepting both camelCase and snake_case and enum *name* strings
(e.g. "STATUS_OK"). We therefore parse leniently (camel or snake) and emit
camelCase + enum names — symmetric with what the Go client sends.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional


@dataclass
class ParsedRequest:
    correlation_id: str
    text: str
    threshold: float
    max_tokens: int  # 0 = use the detector default


@dataclass
class Finding:
    category: str
    score: float
    raw_label: str = ""
    # span is omitted (whole-unit) for sequence classifiers; span detectors
    # (PII) set start/end/basis. None => whole unit.
    start: Optional[int] = None
    end: Optional[int] = None


def parse_request(d: dict) -> ParsedRequest:
    """Parse a protojson DetectRequest, tolerating camelCase or snake_case."""

    def pick(obj: dict, *names, default=None):
        for n in names:
            if n in obj and obj[n] is not None:
                return obj[n]
        return default

    unit = pick(d, "unit", default={}) or {}
    opts = pick(d, "options", default={}) or {}
    return ParsedRequest(
        correlation_id=str(pick(d, "correlationId", "correlation_id", default="")),
        text=str(pick(unit, "text", default="")),
        threshold=float(pick(opts, "threshold", default=0.0) or 0.0),
        max_tokens=int(pick(opts, "maxTokens", "max_tokens", default=0) or 0),
    )


def chunk_windows(total: int, window: int, overlap: int) -> list[tuple[int, int]]:
    """Plan sliding [start, end) token windows over [0, total).

    stride = window - overlap, so adjacent chunks share `overlap` tokens — a
    phrase straddling a boundary lands whole in at least one chunk. The whole
    range is always covered; the final window may be short. overlap is clamped
    to [0, window) to guarantee forward progress.
    """
    if total <= 0:
        return []
    if window <= 0:
        raise ValueError("window must be positive")
    if overlap < 0 or overlap >= window:
        overlap = 0
    if total <= window:
        return [(0, total)]
    stride = window - overlap
    out: list[tuple[int, int]] = []
    start = 0
    while start < total:
        end = min(start + window, total)
        out.append((start, end))
        if end >= total:
            break
        start += stride
    return out


def normalize_scores(raw) -> list[tuple[str, float]]:
    """Flatten a transformers text-classification (top_k=None) result.

    Depending on the transformers version, classifying one string returns either
    a list of {label, score} dicts or a single-element list wrapping that list.
    Normalize both to a flat [(label, score)] list.
    """
    if not raw:
        return []
    if isinstance(raw[0], list):
        raw = raw[0]
    return [(d["label"], float(d["score"])) for d in raw]


def reduce_findings(
    per_chunk: list[list[tuple[str, float]]],
    threshold: float,
    label_map: dict[str, str],
) -> list[Finding]:
    """Merge per-chunk classifier scores into findings (highest-risk-wins).

    per_chunk is one list of (raw_label, score) per chunk. label_map maps a
    positive raw label to a taxonomy category; labels absent from the map are
    benign and ignored. A category present in several overlapping chunks keeps
    its single highest score, so overlap never double-counts.
    """
    best: dict[str, tuple[float, str]] = {}
    for chunk in per_chunk:
        for raw_label, score in chunk:
            category = label_map.get(raw_label)
            if category is None or score < threshold:
                continue
            if category not in best or score > best[category][0]:
                best[category] = (score, raw_label)
    return [
        Finding(category=cat, score=sc, raw_label=lbl)
        for cat, (sc, lbl) in sorted(best.items())
    ]


def _finding_json(f: Finding) -> dict:
    out: dict = {
        "category": f.category,
        "score": f.score,
        "rawLabel": f.raw_label,
        "localization": "LOCALIZATION_NONE",
    }
    if f.start is not None and f.end is not None:
        out["span"] = {"start": f.start, "end": f.end, "basis": "OFFSET_BASIS_UTF8_BYTE"}
        out["localization"] = "LOCALIZATION_EXACT"
    return out


@dataclass
class Scanned:
    tokens: int = 0
    truncated: bool = False
    chunks: int = 0


def build_response(
    correlation_id: str,
    detector: dict,
    findings: list[Finding],
    scanned: Scanned,
    status: str = "STATUS_OK",
    schema_version: str = "v1",
    error: str = "",
) -> dict:
    """Assemble a protojson DetectResponse (camelCase, enum name strings)."""
    resp: dict = {
        "schemaVersion": schema_version,
        "correlationId": correlation_id,
        "detector": {
            "id": detector.get("id", ""),
            "model": detector.get("model", ""),
            "version": detector.get("version", ""),
            "family": detector.get("family", "FAMILY_UNSPECIFIED"),
        },
        "status": status,
        "scanned": {
            "tokens": scanned.tokens,
            "truncated": scanned.truncated,
            "chunks": scanned.chunks,
        },
        "findings": [_finding_json(f) for f in findings],
    }
    if error:
        resp["error"] = error
    return resp


# ---------------------------------------------------------------------------
# PII span detectors (NER) — used by pii_app.py.
#
# Unlike the sequence classifiers above (one label per unit), PII detectors
# return *spans*: typed substrings (email, person, SSN…) at byte offsets. Two
# engines feed one /detect endpoint — OpenAI's openai/privacy-filter token
# classifier and Microsoft Presidio — so their two label vocabularies are
# normalized to one pii.<category> taxonomy here, and their spans are unioned
# (overlapping same-category spans collapse to the highest score). All of this
# is pure and unit-tested; pii_app.py only wires the two models around it.
# ---------------------------------------------------------------------------

# PII_CATEGORY_MAP maps a raw engine entity label (upper-cased, '_'-joined) to a
# canonical pii.<category>. It spans both vocabularies: Presidio's entity types
# (PERSON, EMAIL_ADDRESS, …) and the openai/privacy-filter / ai4privacy-style
# labels (EMAIL, FIRSTNAME, …). Labels absent from the map are NOT dropped —
# pii_category falls back to pii.<lowercased_raw> so a new entity type still
# surfaces as a finding (recall over a fixed taxonomy).
PII_CATEGORY_MAP: dict[str, str] = {
    # people / names
    "PERSON": "pii.person",
    "NAME": "pii.person",
    "FIRSTNAME": "pii.person",
    "LASTNAME": "pii.person",
    "MIDDLENAME": "pii.person",
    "FULLNAME": "pii.person",
    "USERNAME": "pii.username",
    # contact
    "EMAIL": "pii.email",
    "EMAIL_ADDRESS": "pii.email",
    "PHONE": "pii.phone",
    "PHONE_NUMBER": "pii.phone",
    "PHONENUMBER": "pii.phone",
    "PHONEIMEI": "pii.device_id",
    # location / address
    "LOCATION": "pii.location",
    "ADDRESS": "pii.address",
    "STREETADDRESS": "pii.address",
    "STREET": "pii.address",
    "CITY": "pii.location",
    "STATE": "pii.location",
    "COUNTY": "pii.location",
    "ZIPCODE": "pii.location",
    "ZIP": "pii.location",
    "NRP": "pii.nrp",
    # government / financial ids
    "US_SSN": "pii.ssn",
    "SSN": "pii.ssn",
    "US_ITIN": "pii.itin",
    "US_PASSPORT": "pii.passport",
    "US_DRIVER_LICENSE": "pii.drivers_license",
    "CREDIT_CARD": "pii.credit_card",
    "CREDITCARDNUMBER": "pii.credit_card",
    "CREDITCARDCVV": "pii.credit_card_cvv",
    "US_BANK_NUMBER": "pii.bank_account",
    "IBAN_CODE": "pii.iban",
    "IBAN": "pii.iban",
    "BIC": "pii.bic",
    "ACCOUNTNUMBER": "pii.bank_account",
    "MEDICAL_LICENSE": "pii.medical_license",
    # network / device / crypto
    "IP_ADDRESS": "pii.ip_address",
    "IP": "pii.ip_address",
    "IPV4": "pii.ip_address",
    "IPV6": "pii.ip_address",
    "MAC": "pii.mac_address",
    "URL": "pii.url",
    "CRYPTO": "pii.crypto_wallet",
    "ETHEREUMADDRESS": "pii.crypto_wallet",
    "BITCOINADDRESS": "pii.crypto_wallet",
    # temporal / misc identifiers
    "DATE_TIME": "pii.date_time",
    "DATE": "pii.date_time",
    "DOB": "pii.date_of_birth",
    "AGE": "pii.age",
    "PASSWORD": "pii.password",
    "SECRET": "pii.secret",
    "ACCOUNT_NUMBER": "pii.bank_account",
}


def pii_category(raw_label: str) -> str:
    """Normalize a raw engine entity label to a canonical pii.<category>.

    openai/privacy-filter prefixes its entity types with PRIVATE_
    (private_email, private_person, …) where Presidio uses the bare entity
    (EMAIL_ADDRESS, PERSON); stripping the prefix lands both engines on the
    same pii.<category> so merge_spans can collapse cross-engine duplicates.
    Unknown labels fall back to pii.<slug> rather than being dropped, so a new
    entity type a model learns still surfaces as a finding.
    """
    key = raw_label.strip().upper().replace(" ", "_").replace("-", "_")
    candidates = [key]
    if key.startswith("PRIVATE_"):
        candidates.append(key[len("PRIVATE_") :])
    for c in candidates:
        if c in PII_CATEGORY_MAP:
            return PII_CATEGORY_MAP[c]
    slug = candidates[-1].lower()  # PRIVATE_-stripped form when applicable
    return f"pii.{slug}" if slug else "pii.unknown"


def merge_spans(findings: list[Finding], threshold: float) -> list[Finding]:
    """Union PII spans from both engines into one deduped, sorted list.

    Drops sub-threshold findings, then collapses overlapping spans that share a
    category to a single finding keeping the highest score (so OpenAI + Presidio
    both flagging the same email yield one finding, not two). Spans of different
    categories are kept even when they overlap. Findings without offsets
    (whole-unit) are kept as-is, deduped per category to the highest score.
    Operates in whatever offset unit the inputs use (char or byte); pii_app
    merges in char space then converts the survivors with to_byte_spans.
    """
    spanned = [f for f in findings if f.score >= threshold and f.start is not None and f.end is not None]
    whole = [f for f in findings if f.score >= threshold and (f.start is None or f.end is None)]

    # whole-unit: one per category, highest score.
    whole_best: dict[str, Finding] = {}
    for f in whole:
        cur = whole_best.get(f.category)
        if cur is None or f.score > cur.score:
            whole_best[f.category] = f

    # spanned: sort by (start, -end); within a category, merge overlaps.
    spanned.sort(key=lambda f: (f.start, -f.end, f.category))
    out: list[Finding] = []
    for f in spanned:
        merged = False
        for i, g in enumerate(out):
            if g.category == f.category and f.start < g.end and g.start < f.end:
                # overlap, same category → keep the wider span + higher score.
                out[i] = Finding(
                    category=g.category,
                    score=max(g.score, f.score),
                    raw_label=g.raw_label if g.score >= f.score else f.raw_label,
                    start=min(g.start, f.start),
                    end=max(g.end, f.end),
                )
                merged = True
                break
        if not merged:
            out.append(f)

    out.extend(whole_best.values())
    out.sort(key=lambda f: (f.start if f.start is not None else -1, f.category))
    return out


def to_byte_spans(findings: list[Finding], text: str) -> list[Finding]:
    """Convert char-offset findings to UTF-8 byte offsets (the contract basis).

    _finding_json declares OFFSET_BASIS_UTF8_BYTE, so the span numbers the Go
    side stores must be byte offsets. NER engines report char (code-point)
    offsets; this remaps them once, at the boundary. Whole-unit findings (no
    offsets) pass through unchanged.
    """
    # Precompute a cumulative byte-length prefix so each conversion is O(1).
    out: list[Finding] = []
    for f in findings:
        if f.start is None or f.end is None:
            out.append(f)
            continue
        start_b = len(text[: f.start].encode("utf-8"))
        end_b = len(text[: f.end].encode("utf-8"))
        out.append(Finding(category=f.category, score=f.score, raw_label=f.raw_label, start=start_b, end=end_b))
    return out


def coalesce_adjacent(findings: list[Finding], text: str, max_gap: int = 2) -> list[Finding]:
    """Join same-category spans the NER model split into adjacent fragments.

    openai/privacy-filter emits one entity as several touching sub-spans —
    "Marcus Delacro" + "ix", "4002-1199-8830-774" + "1" — which otherwise
    surface as separate findings ("splitting"). This merges consecutive
    same-category spans whose gap in `text` is <= max_gap chars (covering a
    zero-gap subword split and a small separator like ", "), keeping the higher
    score's raw_label. Whole-unit findings (no offsets) pass through.
    """
    spanned = sorted(
        (f for f in findings if f.start is not None and f.end is not None),
        key=lambda f: (f.category, f.start, f.end),
    )
    whole = [f for f in findings if f.start is None or f.end is None]
    out: list[Finding] = []
    for f in spanned:
        if out:
            g = out[-1]
            # same category and f starts within max_gap of g's end (covers a
            # small positive gap and any overlap the earlier merge missed).
            if g.category == f.category and f.start <= g.end + max_gap:
                out[-1] = Finding(
                    category=g.category,
                    score=max(g.score, f.score),
                    raw_label=g.raw_label if g.score >= f.score else f.raw_label,
                    start=g.start,
                    end=max(g.end, f.end),
                )
                continue
        out.append(f)
    out.extend(whole)
    return out


def dedupe_by_value(findings: list[Finding], text: str) -> list[Finding]:
    """Collapse identical (category, substring) spans to one, highest score.

    The captured content often repeats a value within a single unit (the same
    message echoed in the request), so the same entity is detected several times
    ("doubling"). Keep one finding per distinct (category, trimmed text) value.
    Whole-unit findings pass through (deduped per category by merge_spans).
    """
    best: dict[tuple[str, str], Finding] = {}
    passthrough: list[Finding] = []
    for f in findings:
        if f.start is None or f.end is None:
            passthrough.append(f)
            continue
        key = (f.category, text[f.start : f.end].strip())
        cur = best.get(key)
        if cur is None or f.score > cur.score:
            best[key] = f
    return list(best.values()) + passthrough
