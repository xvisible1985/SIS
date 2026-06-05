import type { BotKind } from './types';

export type BotStrategy = 'grid' | 'matrix' | 'signal' | 'scalp' | 'arbitrage' | 'copy' | 'trend' | 'hold';
export type { BotKind };
export type RiskLevel  = 'low' | 'medium' | 'high';
export type TradeMode  = 'spot' | 'futures';
export type RunStatus  = 'running' | 'paused' | 'stopped';

/** Готовый бот в библиотеке */
export type FeaturedBot = {
  id: string;
  name: string;
  /** автор: 'NovaBot' для официальных, username для коммьюнити */
  author: string;
  strategy: BotStrategy;
  risk: RiskLevel;
  /** Verified by NovaBot — синяя галочка */
  verified?: boolean;
  /** Trending — оранжевый бейдж HOT */
  fire?: boolean;
  /** USD/мес. 0 или undefined = бесплатно */
  price: number;
  desc: string;
  /** Например ['BTC','ETH'] — UI добавляет USDT */
  pairs: string[];
  /** Минимальный депозит, USDT */
  minCap: number;
  /** Доп. комиссия автора, % от прибыли (например для copy-trade) */
  fee: number;
  users: number;
  /** Рейтинг 1.0–5.0 */
  rating: number;
  /** Доходность за 30 дней, % */
  perfMonth: number;
  /** Доходность за всё время, % */
  perfTotal: number;
  /** Win-rate, 0–100 */
  winRate: number;
  sharpe: number;
  /** Max drawdown, % (положительное число) */
  drawdown: number;
  /** Точки для спарклайна — минимум 2 */
  spark: number[];
  exchanges: string[];
  lev: number;
  mode: TradeMode;
  tags: string[];
};

/** Бот пользователя — запущенный экземпляр */
export type MyBot = {
  id: string;
  botKind?: BotKind;
  /** ID шаблона из библиотеки или null для кастомных */
  tplId: string | null;
  name: string;
  description: string;
  avatarUrl?: string;
  strategy: BotStrategy;
  status: RunStatus;
  pair: string;
  exchange: string;
  /** Стартовый капитал */
  capital: number;
  started: Date;
  lev: number;
  mode: TradeMode;
  /** "все" | "5" | "3 правила" — сколько символов обрабатывает бот */
  symbolsTotal: string;
  /** Сколько символов сейчас имеют нужный сигнал */
  symbolsWithSignal: string;
  /** Ограничение на одновременную обработку */
  symbolsLimit: string;
  /** Кастомный бот (созданный с нуля) */
  custom?: boolean;
  isOfficial?: boolean;
  approvalStatus: 'pending' | 'approved' | 'rejected' | null;
  activeSecondsAcc: number;
  activeSince: string | null;
  /** Произвольный конфиг — структура зависит от strategy */
  config: Record<string, unknown>;
};

export type BotFilters = {
  q: string;
  strategy: BotStrategy | 'all';
  risk: RiskLevel | 'all';
  mode: TradeMode | 'all';
  sort: 'popular' | 'profit' | 'winrate' | 'newest';
  verified: boolean;
  pricing: 'all' | 'free' | 'paid';
  view: 'grid' | 'list';
};
