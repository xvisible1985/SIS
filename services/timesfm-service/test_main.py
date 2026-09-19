from fastapi.testclient import TestClient

import main


def fake_forecast(series: list[float], horizon: int) -> list[float]:
    last = series[-1]
    return [last * 1.01] * horizon


def test_health():
    client = TestClient(main.app)
    resp = client.get("/health")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


def test_forecast_returns_point_forecast_of_requested_length(monkeypatch):
    monkeypatch.setattr(main, "run_forecast", fake_forecast)
    client = TestClient(main.app)
    resp = client.post("/forecast", json={"series": [1.0, 2.0, 3.0], "horizon": 5})
    assert resp.status_code == 200
    body = resp.json()
    assert len(body["point_forecast"]) == 5
    assert body["point_forecast"][0] == 3.0 * 1.01


def test_forecast_rejects_empty_series(monkeypatch):
    monkeypatch.setattr(main, "run_forecast", fake_forecast)
    client = TestClient(main.app)
    resp = client.post("/forecast", json={"series": [], "horizon": 5})
    assert resp.status_code == 400


def test_forecast_rejects_nonpositive_horizon(monkeypatch):
    monkeypatch.setattr(main, "run_forecast", fake_forecast)
    client = TestClient(main.app)
    resp = client.post("/forecast", json={"series": [1.0, 2.0], "horizon": 0})
    assert resp.status_code == 400
