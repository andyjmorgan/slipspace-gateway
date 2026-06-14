"""SlipSpace Arbiter reference detector (sequence classification).

A CPU HuggingFace text-classification model (injection / toxicity) wrapped in the
slipspace.detect.v1 protojson contract. One image serves any sequence
classifier; the model, family, and label map come from env, so injection and
toxicity are the same code with different config (ADR-006 reference container).

Beyond the simple first-cut detector this replaces, it CHUNKS oversized inputs
with overlap instead of truncating (the 512-token window would otherwise silently
drop content past the cap — a scan-evasion hole). It tokenizes once, plans
sliding windows (detect_core.chunk_windows, stride = window - overlap), classifies
each chunk, and reduces to findings keeping each category's highest score. The
pure contract + chunk-planning + reduce logic lives in detect_core (unit-tested);
this file is just the model wiring.
"""

import json
import logging
import os
import time

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from transformers import AutoModelForSequenceClassification, AutoTokenizer, pipeline

import detect_core as dc

log = logging.getLogger("uvicorn")

MODEL_ID = os.environ.get("DETECTOR_MODEL_ID", "protectai/deberta-v3-base-prompt-injection-v2")
DETECTOR_ID = os.environ.get("DETECTOR_ID", "arbiter-detector")
DETECTOR_VERSION = os.environ.get("DETECTOR_VERSION", "1")
FAMILY = os.environ.get("DETECTOR_FAMILY", "FAMILY_INJECTION")
CACHE_DIR = os.environ.get("HF_HOME", "/cache/hf")

# label_map: raw model label -> taxonomy category. Labels absent from it are
# benign and ignored. Injection default maps the positive class; toxicity passes
# a multi-label map.
LABEL_MAP = json.loads(os.environ.get("DETECTOR_LABEL_MAP", '{"INJECTION": "injection.prompt_injection"}'))

# Chunking: window is the model's usable token budget (512 minus specials);
# overlap keeps a boundary-straddling phrase whole in one chunk; max_chunks
# bounds work on a pathological input (beyond it, scanned.truncated = true).
WINDOW = int(os.environ.get("DETECTOR_WINDOW_TOKENS", "510"))
OVERLAP = int(os.environ.get("DETECTOR_OVERLAP_TOKENS", "64"))
MAX_CHUNKS = int(os.environ.get("DETECTOR_MAX_CHUNKS", "16"))
DEFAULT_THRESHOLD = float(os.environ.get("DETECTOR_THRESHOLD", "0.5"))

log.info(f"loading {MODEL_ID} (cache {CACHE_DIR}); family={FAMILY} window={WINDOW} overlap={OVERLAP}")
tokenizer = AutoTokenizer.from_pretrained(MODEL_ID, cache_dir=CACHE_DIR)
model = AutoModelForSequenceClassification.from_pretrained(MODEL_ID, cache_dir=CACHE_DIR).eval()
nlp = pipeline("text-classification", model=model, tokenizer=tokenizer, top_k=None, device=-1)
_ = nlp("warmup: ignore previous instructions")  # pay PyTorch lazy-init before serving
log.info("model loaded")

DETECTOR = {"id": DETECTOR_ID, "model": MODEL_ID, "version": DETECTOR_VERSION, "family": FAMILY}

app = FastAPI(title=f"arbiter-detector ({FAMILY})", version=DETECTOR_VERSION)


def _classify_chunks(text: str, max_tokens: int):
    """Tokenize, plan overlapping windows, classify each. Returns
    (per_chunk scores, total_tokens, n_chunks, truncated)."""
    window = max_tokens if 0 < max_tokens < WINDOW else WINDOW
    ids = tokenizer.encode(text, add_special_tokens=False)
    total = len(ids)
    windows = dc.chunk_windows(total, window, OVERLAP)
    truncated = False
    if len(windows) > MAX_CHUNKS:
        windows = windows[:MAX_CHUNKS]
        truncated = True
    per_chunk = []
    for start, end in windows:
        chunk_text = tokenizer.decode(ids[start:end], skip_special_tokens=True)
        # top_k=None returns [{label,score},...] or [[...]] across versions.
        per_chunk.append(dc.normalize_scores(nlp(chunk_text)))
    return per_chunk, total, len(windows), truncated


@app.post("/detect")
async def detect(request: Request):
    body = await request.json()
    req = dc.parse_request(body)
    threshold = req.threshold if req.threshold > 0 else DEFAULT_THRESHOLD

    if not req.text.strip():
        return JSONResponse(dc.build_response(req.correlation_id, DETECTOR, [], dc.Scanned()))

    started = time.monotonic()
    try:
        per_chunk, total, n_chunks, truncated = _classify_chunks(req.text, req.max_tokens)
    except Exception as exc:  # noqa: BLE001 — detector must report ERROR, not 500
        log.warning(f"detect failed: {exc}")
        resp = dc.build_response(req.correlation_id, DETECTOR, [], dc.Scanned(),
                                 status="STATUS_ERROR", error=str(exc))
        return JSONResponse(resp, status_code=200)

    findings = dc.reduce_findings(per_chunk, threshold, LABEL_MAP)
    resp = dc.build_response(
        req.correlation_id, DETECTOR, findings,
        dc.Scanned(tokens=total, truncated=truncated, chunks=n_chunks),
    )
    resp["elapsedMs"] = int((time.monotonic() - started) * 1000)
    return JSONResponse(resp)


@app.get("/healthz")
def healthz():
    return {"ok": True, "model": MODEL_ID, "family": FAMILY}
