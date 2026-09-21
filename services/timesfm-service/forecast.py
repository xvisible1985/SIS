"""Wraps the actual TimesFM model. Loaded lazily on first real request so importing this
module (e.g. from tests, or from main.py at process start) never requires the model
checkpoint to be downloaded/present — only a real /forecast call triggers the download."""

import threading

_model = None
_model_lock = threading.Lock()

# The max_horizon TimesFM_2p5_200M_torch is compiled with below — matches the real model's
# own output_patch_len (timesfm.timesfm_2p5.timesfm_2p5_base.TimesFM_2p5_200M_Definition,
# confirmed by reading the installed package's source: output_patch_len=128), so any request
# beyond it must fail loudly instead of silently returning a truncated (too-short) forecast.
_HORIZON_LEN = 128

# Compile-time max context window — must be a multiple of the model's input patch size (32);
# 2048 already is. This is generous headroom over our own default context_bars=512 for a bot
# configured with a higher context_bars (no upper bound is enforced upstream), while staying
# well under the model's hard context_limit (16384). A caller sending more candles than this
# has them silently left-truncated to the most recent _MAX_CONTEXT inside model.forecast()
# (not rejected) — acceptable degradation, not a correctness bug, for a value this generous.
_MAX_CONTEXT = 2048

# google/timesfm-2.5-200m-pytorch is TimesFM_2p5_200M_torch's own DEFAULT_REPO_ID — named
# explicitly here rather than relying on the class default so a future package upgrade that
# changes its default silently doesn't change which checkpoint this service downloads.
_MODEL_REPO_ID = "google/timesfm-2.5-200m-pytorch"


def _load_model():
    global _model
    if _model is None:
        # Double-checked locking: FastAPI runs sync path operations (like main.forecast) in
        # a thread pool, and Go-side refreshes are keyed per symbol/timeframe, not globally —
        # so two different symbols' refreshes can legitimately call in here concurrently.
        # Without a lock, two threads could both observe `_model is None` and both start
        # constructing/downloading the multi-GB checkpoint at once.
        with _model_lock:
            if _model is None:  # re-check inside the lock (another thread may have just finished)
                import timesfm

                model = timesfm.TimesFM_2p5_200M_torch.from_pretrained(_MODEL_REPO_ID)
                model.compile(
                    timesfm.ForecastConfig(
                        max_context=_MAX_CONTEXT,
                        max_horizon=_HORIZON_LEN,
                    )
                )
                _model = model
    return _model


def run_forecast(series: list[float], horizon: int) -> list[float]:
    """Returns a point forecast of length `horizon` for the given closing-price series.

    Uses timesfm==3.0.2's real API (TimesFM 2.5): TimesFM_2p5_200M_torch.from_pretrained(...)
    + .compile(ForecastConfig(...)) + .forecast(horizon=, inputs=) — verified by reading the
    installed package's source directly (services/timesfm-service/.venv/Lib/site-packages/
    timesfm/timesfm_2p5/), since the package's own public docs describe an older 1.x API that
    no longer matches what's actually installed. requirements.txt's timesfm pin and this
    function must be kept in sync with each other if either changes again.

    Raises ValueError if `horizon` exceeds the model's configured max_horizon — callers must
    not silently receive a shorter-than-requested forecast.
    """
    if horizon > _HORIZON_LEN:
        raise ValueError(f"horizon {horizon} exceeds the model's max horizon_len ({_HORIZON_LEN})")
    model = _load_model()
    point_forecast, _ = model.forecast(horizon=horizon, inputs=[series])
    return point_forecast[0][:horizon].tolist()
