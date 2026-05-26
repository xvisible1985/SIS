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

export type StrategyConfig = {
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
