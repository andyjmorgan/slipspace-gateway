# SlipSpace Arbiter reference detectors

A CPU HuggingFace text-classification model wrapped in the `slipspace.detect.v1`
protojson contract. One image serves any sequence classifier (injection,
toxicity); the model and taxonomy come from env, so the two deployed detectors
are this same code with different config (ADR-006 reference container).

Oversized inputs are **chunked with overlap**, not truncated: the 512-token
window would otherwise silently drop content past the cap (a scan-evasion hole).
`detect_core` plans sliding windows (stride = window − overlap), classifies each
chunk, and reduces to findings keeping each category's highest score. The pure
contract + chunk-planning + reduce logic is in `detect_core.py` and is unit
tested; `app.py` is the model wiring.

## Endpoints

- `POST /detect` — body is a protojson `DetectRequest`; returns a `DetectResponse`.
- `GET /healthz`

## Config (env)

| Var | Default | Meaning |
|---|---|---|
| `DETECTOR_MODEL_ID` | `protectai/deberta-v3-base-prompt-injection-v2` | HF model id |
| `DETECTOR_ID` / `DETECTOR_VERSION` | `arbiter-detector` / `1` | provenance on findings |
| `DETECTOR_FAMILY` | `FAMILY_INJECTION` | `FAMILY_INJECTION` \| `FAMILY_TOXICITY` \| … |
| `DETECTOR_LABEL_MAP` | `{"INJECTION":"injection.prompt_injection"}` | raw label → taxonomy category (JSON); unmapped labels are benign |
| `DETECTOR_WINDOW_TOKENS` | `510` | per-chunk token budget (512 minus specials) |
| `DETECTOR_OVERLAP_TOKENS` | `64` | tokens shared between adjacent chunks |
| `DETECTOR_MAX_CHUNKS` | `16` | work bound; beyond it `scanned.truncated = true` |
| `DETECTOR_THRESHOLD` | `0.5` | emit floor when the request sets none |

### Examples

- **injection**: `DETECTOR_FAMILY=FAMILY_INJECTION`, default label map.
- **toxicity**: `DETECTOR_MODEL_ID=unitary/toxic-bert`, `DETECTOR_FAMILY=FAMILY_TOXICITY`,
  `DETECTOR_LABEL_MAP={"toxic":"toxicity.toxic","severe_toxic":"toxicity.severe","obscene":"toxicity.obscene","threat":"toxicity.threat","insult":"toxicity.insult","identity_hate":"toxicity.identity_hate"}`.

## Test

    python3 -m unittest        # detect_core: chunk windows, overlap dedup, contract
