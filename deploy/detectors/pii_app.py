"""SlipSpace Arbiter PII detector (span / NER) — two engines, one endpoint.

Microsoft Presidio and OpenAI's openai/privacy-filter behind one
slipspace.detect.v1 /detect endpoint, unioned:

  - OpenAI    : openai/privacy-filter, a HF token-classification NER model.
  - Microsoft : Presidio (presidio-analyzer + spaCy en_core_web_lg), the
                recognizer-based PII engine.

Each engine yields typed entity spans; their two label vocabularies are
normalized to one pii.<category> taxonomy (detect_core.pii_category) and their
spans are unioned with overlapping same-category spans collapsed to the highest
score (detect_core.merge_spans). One endpoint means this fits the scanner's
single-detector-per-check_type "pii" slot with zero Go changes.

Long inputs are char-windowed with overlap (detect_core.chunk_windows over
character offsets — never truncated): the transformer arm runs per window with
offsets remapped to the original text; Presidio runs on the whole text. Span
offsets are emitted as UTF-8 bytes (the contract basis) via to_byte_spans.

The pure contract + windowing + taxonomy + span-merge logic lives in detect_core
(unit-tested without torch/presidio); this file is just the engine wiring. Each
engine is independently toggleable (PII_OPENAI_ENABLED / PII_PRESIDIO_ENABLED)
so the detector degrades to one engine rather than failing if a model is absent.
"""

import logging
import os
import time

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

import detect_core as dc

log = logging.getLogger("uvicorn")

OPENAI_MODEL_ID = os.environ.get("PII_OPENAI_MODEL_ID", "openai/privacy-filter")
SPACY_MODEL = os.environ.get("PII_SPACY_MODEL", "en_core_web_lg")
DETECTOR_ID = os.environ.get("DETECTOR_ID", "arbiter-pii")
DETECTOR_VERSION = os.environ.get("DETECTOR_VERSION", "1")
FAMILY = os.environ.get("DETECTOR_FAMILY", "FAMILY_PII")
CACHE_DIR = os.environ.get("HF_HOME", "/cache/hf")

OPENAI_ENABLED = os.environ.get("PII_OPENAI_ENABLED", "true").lower() == "true"
PRESIDIO_ENABLED = os.environ.get("PII_PRESIDIO_ENABLED", "true").lower() == "true"

# Char windows (not tokens): char offsets map linearly back to the original
# text, so a span found in window k at local offset o is at o + window_start in
# the original — no wordpiece-to-char remap. The window stays well under the
# 512-token model limit for typical text; overlap keeps a boundary-straddling
# entity whole in one window. max_chunks bounds work on pathological input.
CHAR_WINDOW = int(os.environ.get("PII_CHAR_WINDOW", "2000"))
CHAR_OVERLAP = int(os.environ.get("PII_CHAR_OVERLAP", "200"))
MAX_CHUNKS = int(os.environ.get("PII_MAX_CHUNKS", "32"))
DEFAULT_THRESHOLD = float(os.environ.get("DETECTOR_THRESHOLD", "0.5"))

_openai_nlp = None
_presidio = None

if OPENAI_ENABLED:
    from transformers import AutoModelForTokenClassification, AutoTokenizer, pipeline

    log.info(f"loading openai PII model {OPENAI_MODEL_ID} (cache {CACHE_DIR})")
    _tok = AutoTokenizer.from_pretrained(OPENAI_MODEL_ID, cache_dir=CACHE_DIR)
    _mdl = AutoModelForTokenClassification.from_pretrained(OPENAI_MODEL_ID, cache_dir=CACHE_DIR).eval()
    _openai_nlp = pipeline(
        "token-classification",
        model=_mdl,
        tokenizer=_tok,
        aggregation_strategy="simple",  # merge wordpieces into entity spans
        device=-1,  # CPU
    )
    _ = _openai_nlp("warmup: contact alice@example.com or 555-867-5309")  # pay lazy init
    log.info("openai PII model loaded")

if PRESIDIO_ENABLED:
    from presidio_analyzer import AnalyzerEngine
    from presidio_analyzer.nlp_engine import NlpEngineProvider

    log.info(f"loading presidio (spaCy {SPACY_MODEL})")
    _provider = NlpEngineProvider(
        nlp_configuration={
            "nlp_engine_name": "spacy",
            "models": [{"lang_code": "en", "model_name": SPACY_MODEL}],
        }
    )
    _presidio = AnalyzerEngine(nlp_engine=_provider.create_engine(), supported_languages=["en"])
    _ = _presidio.analyze(text="warmup: contact alice@example.com", language="en")
    log.info("presidio loaded")

if not OPENAI_ENABLED and not PRESIDIO_ENABLED:
    raise RuntimeError("at least one PII engine must be enabled")

_model_desc = "+".join(
    [m for m, on in ((OPENAI_MODEL_ID, OPENAI_ENABLED), (f"presidio:{SPACY_MODEL}", PRESIDIO_ENABLED)) if on]
)
DETECTOR = {"id": DETECTOR_ID, "model": _model_desc, "version": DETECTOR_VERSION, "family": FAMILY}

app = FastAPI(title="arbiter-detector (FAMILY_PII)", version=DETECTOR_VERSION)


def _openai_spans(text: str) -> tuple[list[dc.Finding], int, bool]:
    """Run the openai/privacy-filter NER over char windows; remap offsets to the
    original text. Returns (findings_in_char_offsets, n_chunks, truncated)."""
    windows = dc.chunk_windows(len(text), CHAR_WINDOW, CHAR_OVERLAP)
    truncated = False
    if len(windows) > MAX_CHUNKS:
        windows = windows[:MAX_CHUNKS]
        truncated = True
    out: list[dc.Finding] = []
    for w_start, w_end in windows:
        for r in _openai_nlp(text[w_start:w_end]):
            raw = str(r.get("entity_group") or r.get("entity") or "")
            if not raw:
                continue
            out.append(
                dc.Finding(
                    category=dc.pii_category(raw),
                    score=float(r["score"]),
                    raw_label=f"openai:{raw}",
                    start=w_start + int(r["start"]),
                    end=w_start + int(r["end"]),
                )
            )
    return out, len(windows), truncated


def _presidio_spans(text: str, threshold: float) -> list[dc.Finding]:
    """Run Presidio over the whole text (recognizers handle arbitrary length)."""
    out: list[dc.Finding] = []
    for r in _presidio.analyze(text=text, language="en", score_threshold=threshold):
        out.append(
            dc.Finding(
                category=dc.pii_category(r.entity_type),
                score=float(r.score),
                raw_label=f"presidio:{r.entity_type}",
                start=int(r.start),
                end=int(r.end),
            )
        )
    return out


@app.post("/detect")
async def detect(request: Request):
    body = await request.json()
    req = dc.parse_request(body)
    threshold = req.threshold if req.threshold > 0 else DEFAULT_THRESHOLD

    if not req.text.strip():
        return JSONResponse(dc.build_response(req.correlation_id, DETECTOR, [], dc.Scanned()))

    started = time.monotonic()
    n_chunks, truncated = 1, False
    try:
        raw_findings: list[dc.Finding] = []
        if OPENAI_ENABLED:
            oa, n_chunks, truncated = _openai_spans(req.text)
            raw_findings.extend(oa)
        if PRESIDIO_ENABLED:
            raw_findings.extend(_presidio_spans(req.text, threshold))
    except Exception as exc:  # noqa: BLE001 — detector must report ERROR, not 500
        log.warning(f"pii detect failed: {exc}")
        resp = dc.build_response(
            req.correlation_id, DETECTOR, [], dc.Scanned(), status="STATUS_ERROR", error=str(exc)
        )
        return JSONResponse(resp, status_code=200)

    merged = dc.merge_spans(raw_findings, threshold)
    findings = dc.to_byte_spans(merged, req.text)
    resp = dc.build_response(
        req.correlation_id,
        DETECTOR,
        findings,
        dc.Scanned(tokens=len(req.text), truncated=truncated, chunks=n_chunks),
    )
    resp["elapsedMs"] = int((time.monotonic() - started) * 1000)
    return JSONResponse(resp)


@app.get("/healthz")
def healthz():
    return {"ok": True, "model": _model_desc, "family": FAMILY}
