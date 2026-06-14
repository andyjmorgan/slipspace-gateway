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
