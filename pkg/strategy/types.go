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

type Strategy struct {
	ID           string
	OwnerID      string
	AccountID    string
	Symbol       string
	Category     string
	Direction    Direction
	Status       Status
	GridLevels   int
	GridActive   int
	GridStepPct  float64
	GridSizeUSDT float64
	TPMode       TPMode
	TPPct        float64
	SLType       SLType
	SLPct        float64
	SignalFilter bool
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
