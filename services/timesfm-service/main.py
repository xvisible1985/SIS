from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

import forecast as forecast_module

app = FastAPI()

# Module-level name so tests can monkeypatch it (monkeypatch.setattr(main, "run_forecast",
# fake)) without ever triggering forecast_module's lazy real-model load.
run_forecast = forecast_module.run_forecast


class ForecastRequest(BaseModel):
    series: list[float]
    horizon: int


class ForecastResponse(BaseModel):
    point_forecast: list[float]


@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/forecast", response_model=ForecastResponse)
def forecast(req: ForecastRequest):
    if not req.series:
        raise HTTPException(status_code=400, detail="series must not be empty")
    if req.horizon <= 0:
        raise HTTPException(status_code=400, detail="horizon must be positive")
    try:
        values = run_forecast(req.series, req.horizon)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=503, detail=f"forecast unavailable: {e}")
    return ForecastResponse(point_forecast=values)
