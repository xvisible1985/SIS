package strategy

import "time"

type Status string

const (
	StatusActive    Status = "active"
	StatusFinishing Status = "finishing"
	StatusStopped   Status = "stopped"
	StatusPaused    Status = "paused"
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

// SignalConfig identifies one leg of a strategy's signal filter/TP-signal/SL-signal.
// Exactly one of Name or CustomSignalID is meaningful: a bare catalog reference (Name set,
// e.g. "rsi-os") evaluates directly via the pkg/signal registry; a saved combo reference
// (CustomSignalID set — see migrations/091_custom_signals.sql) expands into that combo's
// own component legs at evaluation time (resolveSignalConfigs in custom_signal.go), so
// editing the combo later changes every strategy that references it. Params only applies
// to the Name case — a combo's legs each carry their own saved params.
type SignalConfig struct {
	Name           string                 `json:"name"`
	Params         map[string]interface{} `json:"params"`
	CustomSignalID string                 `json:"custom_signal_id,omitempty"`
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
	UseSignal      bool     `json:"use_signal,omitempty"` // gate this level on signal before placing
}

type MatrixEntryLevel struct {
	PriceStepPct   *float64 `json:"price_step_pct,omitempty"`
	SizePct        float64  `json:"size_pct"`
	StopPct        *float64 `json:"stop_pct,omitempty"`
	StopCondPct    *float64 `json:"stop_cond_pct,omitempty"`
	StopReplacePct *float64 `json:"stop_replace_pct,omitempty"`
	TPPct          *float64 `json:"tp_pct,omitempty"`
	OrderType      string   `json:"order_type,omitempty"` // "exchange" | "virtual"
	UseSignal      bool     `json:"use_signal,omitempty"` // gate entry level on signal before placing
}

// AlsThreshold is one row of an ALS strategy's "Пороги" (thresholds) table — see
// docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md. TriggerType mirrors
// the existing hedge-activation act_type convention (0 = % from the last filled order,
// 1 = % from the main position's average entry price) rather than the source ALS's own
// ROI-based units — a deliberate adaptation, not an oversight.
type AlsThreshold struct {
	TriggerType    int     `json:"trigger_type"`    // 0 = from last order, 1 = from avg entry (ТВХ)
	TriggerPct     float64 `json:"trigger_pct"`      // negative = adverse move, matches act_value convention
	TargetHedgePct float64 `json:"target_hedge_pct"` // cumulative target hedge volume, % of main's CURRENT live volume
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
	RelativeSlots         bool // Novabot-style: после SL слоты перенумеровываются к точке входа; взаимоисключимо с RebuildOnSL/RebuildFromEntry
	SizeAsMain            bool // Deposit = Main-position volume: each slot sized against opposite-direction position's USDT value

	// ALS (Anti-Liquidation System) hedge strategy fields — meaningful only when
	// StrategyType == "als". See docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md.
	AlsThresholds     []AlsThreshold
	UnsafeHedgeMinPct float64
	UnsafeHedgeMaxPct float64
	StopMarketSizeK   float64
	TPControlEnabled  bool
	InitialStopBEPct  *float64
	AlsStopCondPct    float64 // global "Min ROI for hedge stop" — % price move in the hedge's favor before a stop is placed/replaced
	AlsStopReplacePct float64 // global "Stop Loss Break Even Hedge" — the resulting stop trigger, % behind AlsStopCondPct
	// AlsExchangeOrders: when true, pending top-up levels are placed as real conditional
	// (stop-market) orders on the exchange at their live computed target_price — repriced
	// as the main position's avg entry moves — instead of the default reactive behavior
	// (alsPriceTick fires a plain market order only once price has already crossed the
	// threshold, which can fill far from and out of order relative to the intended targets
	// during a fast move). See alsPriceTick/alsSyncExchangeLevel.
	AlsExchangeOrders bool

	// AdoptPositionData — when non-nil, startMatrixCycle marks L(0) as already-filled
	// using this position snapshot; cleared from the strategy record after consumption.
	AdoptPositionData *AdoptPositionData

	// Hedge main control flags — set by hedge engine when a hedge activates/deactivates.
	HedgeTpSuppressed bool    // do not place/re-place TP orders while true
	HedgeSlSuppressed bool    // do not place/re-place SL orders while true
	HedgeStoppedBy    *string // ID of the hedge strategy that stopped this main; nil if not stopped by hedge

	// Signal-gated exit: position closes when all signals fire in the required direction.
	// dir="" means auto (long→sell, short→buy). Multiple configs use AND logic.
	TPSignalConfigs []SignalConfig
	TPSignalDir     string // "buy" | "sell" | "" (auto)
	SLSignalConfigs []SignalConfig
	SLSignalDir     string // "buy" | "sell" | "" (auto)
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
	SLLinkID     string // our own orderLinkId for SLOrderID — lets matrixSLSeq be restored across restarts
	SLPrice      float64
	SLReplaced   bool
	Slot         *int      // nil = grid; matrix slot index: -N…0…+N
	ForceVirtual bool      // set at runtime when exchange rejected placement (e.g. 110007)
	UseSignal    bool      // from GridStep/MatrixLevel.UseSignal — gate this level's order on signal
	PlacedAt     time.Time // in-memory: when order was last placed (for interference detection)
}

// AdoptPositionData carries the exchange-position snapshot that the next matrix
// startMatrixCycle should absorb instead of placing a market L(0) order.
// Cleared from the DB by startMatrixCycle after it is consumed.
type AdoptPositionData struct {
	Size       string `json:"size"`        // position qty string (e.g. "35")
	EntryPrice string `json:"entry_price"` // avg entry price string (e.g. "0.4647")
}
