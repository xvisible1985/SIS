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
  grid_levels?: number;
  grid_active?: number;
  grid_step_pct?: number;
  grid_size_usdt?: number;
  tp_mode?: 'per_level' | 'total';
  tp_pct?: number;
  sl_type?: 'conditional' | 'programmatic';
  sl_pct?: number;
  signal_filter?: boolean;
};

export type Bot = {
  id: string;
  name: string;
  description: string;
  ownerId: string;
  ownerName: string;
  isOwn: boolean;
  isPublic: boolean;
  status: BotStatus;
  sourceBotId: string | null;
  isFork: boolean;
  symbolWhitelist: string[];
  symbolBlacklist: string[];
  triggers: Trigger[];
  strategyConfig: StrategyConfig;
  deployCount: number;
  createdAt: Date;
};

export type CreateBotInput = {
  name: string;
  description?: string;
  isPublic?: boolean;
  symbolWhitelist?: string[];
  symbolBlacklist?: string[];
  triggers?: Trigger[];
  strategyConfig?: StrategyConfig;
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
