export type Account = {
  exchangeBadge: string;
  name: string;
  exchange: 'Bybit' | 'Binance' | 'OKX' | (string & {});
  status: 'подключено' | 'отключено' | 'ошибка' | (string & {});
};

export type Pnl = {
  percent: number;
  usd: number;
};

export type User = {
  initials: string;
  name: string;
  email: string;
};

export type SidebarProps = {
  version?: string;
  account: Account;
  equity: number;
  pnl24h: Pnl;
  spark: number[];
  novabotBalance: number;
  user: User;
  counters?: Partial<Record<'webhooks' | 'accounts', number>>;
  onSelectAccount?: () => void;
  onTopUp?: () => void;
  onOpenSettings?: () => void;
  onLogout?: () => void;
};
