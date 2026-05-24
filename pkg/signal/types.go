package signal

import "encoding/json"

// State is the output of a signal computation.
type State string

const (
	Buy     State = "buy"
	Sell    State = "sell"
	Neutral State = "neutral"
)

// Candle is a single OHLCV bar.
type Candle struct {
	Time   int64
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

// Config identifies a signal by name + mixed params (numbers and strings).
type Config struct {
	Name   string                 `json:"name"`
	Params map[string]interface{} `json:"params"`
}

// Float returns a numeric param, falling back to def when absent.
func (c Config) Float(key string, def float64) float64 {
	v, ok := c.Params[key]
	if !ok {
		return def
	}
	switch val := v.(type) {
	case float64:
		return val
	case json.Number:
		f, _ := val.Float64()
		return f
	case int:
		return float64(val)
	case int64:
		return float64(val)
	}
	return def
}

// Str returns a string param, falling back to def when absent.
func (c Config) Str(key string, def string) string {
	v, ok := c.Params[key]
	if !ok {
		return def
	}
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

// Signal is implemented by every signal computer.
type Signal interface {
	Compute(candles []Candle) State
}

// SignalValuer is an optional extension for signals that expose a meaningful
// numeric current value (e.g. RSI = 49.5, Stochastic K = 32.1).
type SignalValuer interface {
	Signal
	Value(candles []Candle) float64
}

// TTLAware is an optional extension for signals with a configurable time-to-live.
// TTLRemainingSec returns seconds left; -1 means no TTL configured or not yet fired.
type TTLAware interface {
	TTLRemainingSec() float64
}
