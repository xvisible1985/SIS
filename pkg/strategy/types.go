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
	LevelSLClosed  LevelStatus = "sl_closed"
)

type SignalConfig struct {
	Name   string                 `json:"name"`
	Params map[string]interface{} `json:"params"`
}

type GridStep struct {
	PriceMovePct float64 `json:"price_move_pct"`
	SizePct      float64 `json:"size_pct"`
	Lots         float64 `json:"lots,omitempty"`       // legacy field — migrated from old format
	OrderType    string  `json:"order_type,omitempty"` // "exchange" | "virtual"
	UseSignal    bool    `json:"use_signal,omitempty"` // gate this level on signal before placing
}

type MatrixLevel struct {
	Direction      string   `json:"direction"` // "above" | "below"
	PriceStepPct   float64  `json:"price_step_pct"`
	SizePct        float64  `json:"size_pct"`
	StopPct        *float64 `json:"stop_pct,omitempty"`
	StopCondPct    *float64 `json:"stop_cond_pct,omitempty"`
	StopReplacePct *float64 `json:"stop_replace_pct,omitempty"`
	TPPct          *float64 `json:"tp_pct,omitempty"`
	OrderType      string   `json:"order_type,omitempty"` // "exchange" | "virtual"
}

type MatrixEntryLevel struct {
	PriceStepPct   *float64 `json:"price_step_pct,omitempty"`
	SizePct        float64  `json:"size_pct"`
	StopPct        *float64 `json:"stop_pct,omitempty"`
	StopCondPct    *float64 `json:"stop_cond_pct,omitempty"`
	StopReplacePct *float64 `json:"stop_replace_pct,omitempty"`
	TPPct          *float64 `json:"tp_pct,omitempty"`
	OrderType      string   `json:"order_type,omitempty"` // "exchange" | "virtual"
}

type Strategy struct {
	ID                    string
	OwnerID               string
	AccountID             string
	BotID                 *string
	Symbol                string
	Category              string
	Direction             Direction
	Status                Status
	GridLevels            int
	GridActive            int
	MaxStopActive         int
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
	MaxCycles             int
	CycleCount            int
	EntryOrderType        string
	ManualAlert           string
	MatrixLevels          []MatrixLevel
	SafeZonePct           float64
	MatrixEntryLevel      *MatrixEntryLevel
	ProtectedBuild        bool
	RebuildOnSL           bool // Перестройка сетки от SZ: после SL немедленно переставить уровни от нижней границы SZ
	RebuildFromEntry      bool // Якорь на точку входа: все уровни строятся от цены заполнения L(0); SL L(0) тоже ждёт SZ и перезаходит
	SizeAsMain            bool // Deposit = Main-position volume: each slot sized against opposite-direction position's USDT value

	// Hedge main control flags — set by hedge engine when a hedge activates/deactivates.
	HedgeTpSuppressed bool    // do not place/re-place TP orders while true
	HedgeSlSuppressed bool    // do not place/re-place SL orders while true
	HedgeStoppedBy    *string // ID of the hedge strategy that stopped this main; nil if not stopped by hedge
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

type MatrixSafeZone struct {
	Low, High float64
	SLTrigger float64 // original SL trigger price = re-entry threshold on negative exit
	Slot      int     // slot index whose per-level SL fired
	CreatedAt time.Time
}

// Contains reports whether price falls within the Safe Zone bounds (inclusive).
func (z *MatrixSafeZone) Contains(price float64) bool {
	return price >= z.Low && price <= z.High
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
	ExchangeLinkID  string
	FilledPrice     float64
	// Matrix-only fields (nil/zero for grid strategies)
	SLOrderID    string
	SLPrice      float64
	SLReplaced   bool
	Slot         *int // nil = grid; matrix slot index: -N…0…+N
	ForceVirtual bool // set at runtime when exchange rejected placement (e.g. 110007)
}
