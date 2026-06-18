package signal

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

// Factory builds a Signal from a Config.
type Factory func(cfg Config) Signal

var registry = map[string]Factory{}

// Register adds a signal factory under name.
func Register(name string, f Factory) { registry[name] = f }

// Build constructs a Signal from cfg, returning an error for unknown names.
func Build(cfg Config) (Signal, error) {
	f, ok := registry[cfg.Name]
	if !ok {
		return nil, fmt.Errorf("signal: unknown %q", cfg.Name)
	}
	return f(cfg), nil
}

// HashConfigs produces a stable SHA-256 hex key for a (symbol, interval, []Config) tuple.
// Configs are canonicalised: sorted by name, params sorted by key, floats rounded to 6dp.
func HashConfigs(symbol, interval string, configs []Config) string {
	type canonParam struct {
		K string      `json:"k"`
		V interface{} `json:"v"`
	}
	type canonCfg struct {
		Name   string       `json:"name"`
		Params []canonParam `json:"params"`
	}
	canonical := make([]canonCfg, len(configs))
	for i, cfg := range configs {
		keys := make([]string, 0, len(cfg.Params))
		for k := range cfg.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		params := make([]canonParam, len(keys))
		for j, k := range keys {
			v := cfg.Params[k]
			// Round floats to 6 decimal places to avoid fp noise
			if f, ok := toFloat(v); ok {
				v = math.Round(f*1e6) / 1e6
			}
			params[j] = canonParam{K: k, V: v}
		}
		canonical[i] = canonCfg{Name: cfg.Name, Params: params}
	}
	// Sort by name for order-independence
	sort.Slice(canonical, func(i, j int) bool {
		return canonical[i].Name < canonical[j].Name
	})
	data, _ := json.Marshal(map[string]interface{}{
		"symbol":   symbol,
		"interval": interval,
		"configs":  canonical,
	})
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:8]) // 16-char hex — unique enough for display
}

func toFloat(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case json.Number:
		f, err := val.Float64()
		return f, err == nil
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	}
	return 0, false
}

// ── init: register all built-in signals ───────────────────────────────────

func init() {
	Register("rsi-os", func(cfg Config) Signal {
		return &rsiOversold{
			period:    int(cfg.Float("period", 14)),
			threshold: cfg.Float("threshold", 30),
			kind:      cfg.Str("kind", "cross"),
		}
	})

	Register("macd-x", func(cfg Config) Signal {
		return &macdCross{
			fast:      int(cfg.Float("fast", 12)),
			slow:      int(cfg.Float("slow", 26)),
			sigPeriod: int(cfg.Float("signal", 9)),
			dir:       cfg.Str("dir", "вверх"),
		}
	})

	Register("gc", func(cfg Config) Signal {
		return &goldenCross{
			fast:    int(cfg.Float("fast", 50)),
			slow:    int(cfg.Float("slow", 200)),
			confirm: cfg.Str("confirm", "1bar"),
		}
	})

	Register("bb-sq", func(cfg Config) Signal {
		return &bbSqueeze{
			period: int(cfg.Float("period", 20)),
			std:    cfg.Float("std", 2.0),
			width:  cfg.Float("width", 3.0),
		}
	})

	Register("stoch-x", func(cfg Config) Signal {
		return &stochCross{
			k:    int(cfg.Float("k", 14)),
			d:    int(cfg.Float("d", 3)),
			zone: cfg.Float("zone", 20),
		}
	})

	Register("vol-spike", func(cfg Config) Signal {
		return &volSpike{
			period: int(cfg.Float("period", 20)),
			mult:   cfg.Float("mult", 2.5),
			candle: cfg.Str("candle", "любая"),
		}
	})

	Register("breakout", func(cfg Config) Signal {
		return &rangeBreakout{
			period: int(cfg.Float("period", 20)),
			buffer: cfg.Float("buffer", 0.10),
			dir:    cfg.Str("dir", "up"),
		}
	})

	Register("ema-x", func(cfg Config) Signal {
		return &emaCross{
			fast: int(cfg.Float("fast", 9)),
			slow: int(cfg.Float("slow", 21)),
			dir:  cfg.Str("dir", "вверх"),
		}
	})

	Register("div", func(cfg Config) Signal {
		return &rsiDivergence{
			period:   int(cfg.Float("period", 14)),
			lookback: int(cfg.Float("lookback", 50)),
			dir:      cfg.Str("dir", "bull"),
		}
	})

	Register("st-flip", func(cfg Config) Signal {
		ttl := time.Duration(cfg.Float("ttl", 0) * float64(time.Minute))
		return &superTrendFlip{
			atrPeriod: int(cfg.Float("atr", 10)),
			mult:      cfg.Float("mult", 3.0),
			dir:       cfg.Str("dir", "bull"),
			ttl:       ttl,
		}
	})

	// ── Indicator IDs — registered when admin moves them to the signals panel ──

	Register("rsi-test", func(cfg Config) Signal {
		return &rsiTest{
			period:    int(cfg.Float("period", 14)),
			threshold: cfg.Float("threshold", 30),
			kind:      cfg.Str("kind", "stay"),
		}
	})

	Register("rsi", func(cfg Config) Signal {
		return &rsiZone{
			period: int(cfg.Float("period", 14)),
			lower:  cfg.Float("lower", 30),
			upper:  cfg.Float("upper", 70),
		}
	})

	Register("macd", func(cfg Config) Signal {
		return &macdTrend{
			fast:      int(cfg.Float("fast", 12)),
			slow:      int(cfg.Float("slow", 26)),
			sigPeriod: int(cfg.Float("signal", 9)),
		}
	})

	Register("ema", func(cfg Config) Signal {
		return &emaTrend{period: int(cfg.Float("period", 20))}
	})

	Register("sma", func(cfg Config) Signal {
		return &smaTrend{period: int(cfg.Float("period", 20))}
	})

	Register("bb", func(cfg Config) Signal {
		return &bbZone{
			period: int(cfg.Float("period", 20)),
			std:    cfg.Float("std", 2.0),
		}
	})

	Register("stoch", func(cfg Config) Signal {
		d := int(cfg.Float("smooth", 0))
		if d == 0 {
			d = int(cfg.Float("d", 3))
		}
		return &stochTrend{k: int(cfg.Float("k", 14)), d: d}
	})

	Register("st", func(cfg Config) Signal {
		ttl := time.Duration(cfg.Float("ttl", 0) * float64(time.Minute))
		return &superTrendContinuous{
			atrPeriod: int(cfg.Float("atr", 10)),
			mult:      cfg.Float("mult", 3.0),
			ttl:       ttl,
		}
	})

	// ADX — trend-strength filter.
	// Returns Buy when ADX ≥ threshold AND +DI > -DI (uptrend).
	// Returns Sell when ADX ≥ threshold AND -DI > +DI (downtrend).
	// Returns Neutral when ADX < threshold (no trend / ranging).
	Register("adx", func(cfg Config) Signal {
		return &adxSignal{
			period:    int(cfg.Float("period", 14)),
			threshold: cfg.Float("threshold", 25),
			mode:      cfg.Str("mode", "trend"),
		}
	})
}
