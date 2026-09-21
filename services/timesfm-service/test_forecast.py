import pytest

import forecast


def test_run_forecast_rejects_horizon_exceeding_max():
    # horizon check happens before _load_model() is ever called, so this must raise without
    # needing the real timesfm package or model weights present.
    with pytest.raises(ValueError, match="exceeds the model's max horizon_len"):
        forecast.run_forecast([1.0, 2.0, 3.0], forecast._HORIZON_LEN + 1)


class _FakeArray(list):
    """Minimal stand-in for a numpy array: slicing stays a _FakeArray (unlike a plain list,
    which would degrade to `list` on slicing and lose `.tolist()`), matching what
    forecast.run_forecast's `point_forecast[0][:horizon].tolist()` expects from the real
    numpy-backed model output — without requiring numpy to be installed just for this test.
    """

    def __getitem__(self, item):
        result = super().__getitem__(item)
        return _FakeArray(result) if isinstance(item, slice) else result

    def tolist(self):
        return list(self)


def test_run_forecast_allows_horizon_at_max(monkeypatch):
    class FakeModel:
        def forecast(self, horizon, inputs):
            return [_FakeArray(float(i) for i in range(forecast._HORIZON_LEN))], None

    monkeypatch.setattr(forecast, "_load_model", lambda: FakeModel())
    result = forecast.run_forecast([1.0, 2.0, 3.0], forecast._HORIZON_LEN)
    assert len(result) == forecast._HORIZON_LEN
