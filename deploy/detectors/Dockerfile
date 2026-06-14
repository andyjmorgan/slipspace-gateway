# Reference Arbiter detector image (ADR-006/ADR-013). One image serves any
# sequence classifier; model + family + label_map come from env (see README).
# Baked deps (vs the pip-at-init configmap) for fast, airgap-safe cold starts.
FROM python:3.12-slim

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY detect_core.py app.py ./
ENV HF_HOME=/cache/hf
EXPOSE 8000
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
