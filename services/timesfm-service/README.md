# TimesFM forecast service

Experimental signal backend for `pkg/signal`'s `timesfm` signal — wraps Google's TimesFM
model behind a small HTTP API. Not containerized yet; run it directly:

    cd services/timesfm-service
    python -m venv .venv
    .venv/Scripts/activate   # or `source .venv/bin/activate` on Linux/Mac
    pip install -r requirements.txt
    uvicorn main:app --host 0.0.0.0 --port 8500

Set `TIMESFM_SERVICE_URL` in the project's `.env` if you run it on a different host/port
than `http://localhost:8500` (what `services/api-gateway` falls back to by default).

## API

- `GET /health` — `{"status": "ok"}` once the process is up (does NOT mean the model is
  loaded — that only happens lazily, on the first `/forecast` call).
- `POST /forecast` — `{"series": [float, ...], "horizon": int}` →
  `{"point_forecast": [float, ...]}` (length == horizon).

## First real request

The first `/forecast` call triggers a model checkpoint download from Hugging Face
(multi-GB) and loads it into memory — expect this to take a while, and expect several GB
of RAM in use afterward. Subsequent calls reuse the already-loaded model.

## Testing

`pytest -v` from this directory — the test suite monkeypatches the forecast call, so it
never needs the real model weights downloaded.
