"""Wraps the actual TimesFM model. Loaded lazily on first real request so importing this
module (e.g. from tests, or from main.py at process start) never requires the model
checkpoint to be downloaded/present — only a real /forecast call triggers the download."""

_model = None


def _load_model():
    global _model
    if _model is None:
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
    """
    model = _load_model()
    point_forecast, _ = model.forecast([series], freq=[0])
    return point_forecast[0][:horizon].tolist()
