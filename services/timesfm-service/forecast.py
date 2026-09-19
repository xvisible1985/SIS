"""Wraps the actual TimesFM model. Loaded lazily on first real request so importing this
module (e.g. from tests, or from main.py at process start) never requires the model
checkpoint to be downloaded/present — only a real /forecast call triggers the download."""

import threading

_model = None
_model_lock = threading.Lock()

# The horizon_len TimesFm is constructed with below — the model never produces more points
# than this per forecast() call, so any request beyond it must fail loudly instead of
# silently returning a truncated (too-short) forecast.
_HORIZON_LEN = 128


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

                _model = timesfm.TimesFm(
                    hparams=timesfm.TimesFmHparams(
                        backend="cpu",
                        horizon_len=128,
                    ),
                    checkpoint=timesfm.TimesFmCheckpoint(
                        huggingface_repo_id="google/timesfm-1.0-200m",
                    ),
                )
    return _model


def run_forecast(series: list[float], horizon: int) -> list[float]:
    """Returns a point forecast of length `horizon` for the given closing-price series.

    NOTE: this calls into the real `timesfm` package, whose exact API (TimesFmHparams /
    TimesFmCheckpoint / forecast() argument names) may have changed since this was written —
    verify against the installed package's own README/docstrings before relying on this in
    production, and adjust this function if the constructor or forecast() signature differs.

    Raises ValueError if `horizon` exceeds the model's configured horizon_len — callers must
    not silently receive a shorter-than-requested forecast.
    """
    if horizon > _HORIZON_LEN:
        raise ValueError(f"horizon {horizon} exceeds the model's max horizon_len ({_HORIZON_LEN})")
    model = _load_model()
    point_forecast, _ = model.forecast([series], freq=[0])
    return point_forecast[0][:horizon].tolist()
