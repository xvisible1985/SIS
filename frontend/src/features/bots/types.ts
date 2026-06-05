import type { MatrixLevel, MatrixEntryLevel } from '../../types';
export type { MatrixLevel, MatrixEntryLevel };

export type BotStatus = 'active' | 'stopped' | 'draft';

export type TriggerSignal = {
  type: 'signal';
  signal_id: string;
  condition: 'buy' | 'sell' | 'neutral';
};

export type TriggerPnl = {
  type: 'pnl';
  direction: 'long' | 'short';
  threshold_pct: number;
};

export type Trigger = TriggerSignal | TriggerPnl;

export type BotKind = 'signal' | 'parser' | 'hedge';

export type StrategyConfig = {
  bot_kind?: BotKind;
  symbol?: string;
  category?: string;
  direction?: 'long' | 'short' | 'both';
  strategy_type?: 'grid' | 'matrix';
  entry_order_type?: 'limit' | 'stop_market';
  leverage?: number;
  margin_type?: 'isolated' | 'cross';
  hedge_mode?: boolean;
  grid_levels?: number;
  grid_active?: number;
  grid_step_pct?: number;
  grid_size_usdt?: number;
  steps?: { price_move_pct: number; lots?: number; size_pct?: number }[];
  signal_configs?: { name: string; params?: Record<string, unknown> }[];
  activation_signals?: { name: string; params?: Record<string, unknown> }[];
  tp_mode?: 'per_level' | 'total';
  tp_pct?: number;
  sl_type?: 'conditional' | 'programmatic';
  sl_pct?: number;
  signal_filter?: boolean;
  trailing_stop_enabled?: boolean;
  trailing_activation_pct?: number;
  trailing_callback_pct?: number;
  after_stop_mode?: 'delete' | 'restart';
  max_cycles?: number;
  priority_signal?: string;
  // Signal-gated exit
  tp_signal_name?: string;
  tp_signal_dir?: 'buy' | 'sell';
  sl_signal_name?: string;
  sl_signal_dir?: 'buy' | 'sell';
  // Matrix-specific
  matrix_levels?: MatrixLevel[];
  matrix_entry_level?: MatrixEntryLevel;
  safe_zone_pct?: number;
  protected_build?: boolean;
  matrix_rebuild_on_sl?: boolean;
  matrix_rebuild_from_entry?: boolean;
  size_as_main?: boolean;
  // Hedge bot activation (see HedgeBotForm)
  hedge_act_type?: number;          // 0=last_order%, 1=drawdown%, 2=pnl$, 3=roi%
  hedge_act_value?: number;
  hedge_sig_enable?: boolean;
  hedge_sig_name?: string;
  hedge_sig_dir?: 'buy' | 'sell';
  hedge_sig_dt_hours?: number;
  hedge_close_type?: number;        // 0=at_cycle_end, 1=max_loss$
  hedge_close_value?: number;
  hedge_deact_close_type?: number;  // 0=pnl$, 1=roi%, 2=breakeven
  hedge_deact_close_value?: number;
  hedge_profit_lazy?: boolean;
  hedge_profit_lazy_pct?: number;
  hedge_deact_type?: number;        // 0=drawdown%, 1=pnl$, 2=roi%, 3=last_order%, 4=wait_pair
  hedge_deact_value?: number;
  hedge_bot_whitelist?: string[];   // bot IDs to allow (empty = any)
  hedge_bot_blacklist?: string[];   // bot IDs to exclude
  // Hedge → Main control actions
  hedge_cancel_main_tp?: boolean;   // cancel TP orders on main bot when hedge activates
  hedge_cancel_main_sl?: boolean;   // cancel SL orders on main bot when hedge activates
  hedge_stop_main?: boolean;        // move main bot to "stopped" state and cancel all orders
};

export type Bot = {
  id: string;
  name: string;
  description: string;
  fullDescription?: string;
  avatarUrl?: string;
  ownerId: string;
  ownerName: string;
  isOwn: boolean;
  isPublic: boolean;
  isOfficial: boolean;
  status: BotStatus;
  sourceBotId: string | null;
  isFork: boolean;
  symbolWhitelist: string[];
  symbolBlacklist: string[];
  triggers: Trigger[];
  strategyConfig: StrategyConfig;
  deployCount: number;
  createdAt: Date;
  maxStrategies: number;
  maxLongStrategies: number;
  maxShortStrategies: number;
  maxMarginUsdt: number;
  maxSymConsecutiveRuns: number;
  activeStrategiesCount: number;
  accountId: string | null;
  autoMode: boolean;
  ignoreCoinFilter?: boolean;
  custom?: boolean;
};

export type CreateBotInput = {
  name: string;
  description?: string;
  fullDescription?: string;
  isPublic?: boolean;
  avatarUrl?: string;
  symbolWhitelist?: string[];
  symbolBlacklist?: string[];
  triggers?: Trigger[];
  strategyConfig?: StrategyConfig;
  maxStrategies?: number;
  maxLongStrategies?: number;
  maxShortStrategies?: number;
  maxMarginUsdt?: number;
  maxSymConsecutiveRuns?: number;
  accountId?: string | null;
  autoMode?: boolean;
  ignoreCoinFilter?: boolean;
};

export type BotFilters = {
  q: string;
  direction: 'all' | 'long' | 'short' | 'both';
  sort: 'new' | 'popular';
};

export type BotAction =
  | { type: 'start';   botId: string }
  | { type: 'stop';    botId: string }
  | { type: 'deploy';  botId: string; symbolWhitelist?: string[]; symbolBlacklist?: string[] }
  | { type: 'fork';    botId: string }
  | { type: 'publish'; botId: string }
  | { type: 'update';  botId: string; data: Partial<CreateBotInput> }
  | { type: 'delete';  botId: string }
  | { type: 'create';  data: CreateBotInput };
