package strategy

import "time"

type Status string

const (
	StatusActive    Status = "active"
	StatusFinishing Status = "finishing"
	StatusStopped   Status = "stopped"
)

type Direction string

const (
	DirectionLong  Direction = "long"
	DirectionShort Direction = "short"
	DirectionBoth  Direction = "both"
)

type TPMode string

const (
	TPModeTotal    TPMode = "total"
	TPModePerLevel TPMode = "per_level"
)

type SLType string

const (
	SLTypeConditional  SLType = "conditional"
	SLTypeProgrammatic SLType = "programmatic"
)

type LevelStatus string

const (
	LevelPending   LevelStatus = "pending"
	LevelPlaced    LevelStatus = "placed"
	LevelFilled    LevelStatus = "filled"
	LevelCancelled LevelStatus = "cancelled"
)

type SignalConfig struct {
	Name   string                 `json:"name"`
	Params map[string]interface{} `json:"params"`
}

type GridStep struct {
	PriceMovePct float64 `json:"price_move_pct"`
	Lots         float64 `json:"lots"`
}

type Strategy struct {
	ID                    string
	OwnerID               string
	AccountID             string
	Symbol                string
	Category              string
	Direction             Direction
	Status                Status
	GridLevels            int
	GridActive            int
	GridStepPct           float64
	GridSizeUSDT          float64
	TPMode                TPMode
	TPPct                 float64
	SLType                SLType
	SLPct                 float64
	SignalFilter          bool
	Leverage              int
	MarginType            string
	HedgeMode             bool
	StrategyType          string
	SignalConfigs         []SignalConfig
	Steps                 []GridStep
	TrailingStopEnabled   bool
	TrailingActivationPct float64
	TrailingCallbackPct   float64
	EntryOrderType        string
}

type Cycle struct {
	ID         string
	StrategyID string
	CycleNum   int
	StartPrice float64
	TPOrderID  string
	SLOrderID  string
	StartedAt  time.Time
}

type GridLevel struct {
	ID              string
	LevelIdx        int
	Side            string
	TargetPrice     float64
	SizeUSDT        float64
	Qty             string
	Status          LevelStatus
	ExchangeOrderID string
	FilledPrice     float64
}
