export type Account = {
  exchangeBadge: string;
  name: string;
  exchange: 'Bybit' | 'Binance' | 'OKX' | (string & {});
  status: 'подключено' | 'отключено' | 'ошибка' | (string & {});
  // Risk-monitor state (see AccountsPage's risk-settings section for the underlying
  // margin_warn_pct/margin_pause_pct thresholds) — surfaced here so it's visible from
  // every page, not just when the account card happens to be expanded.
  riskPaused?: boolean;
  riskWarning?: boolean;
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

export type PickerAccount = {
  id: string;
  exchangeBadge: string;
  name: string;
  exchange: string;
};

export type SidebarProps = {
  version?: string;
  account: Account;
  pickerAccounts?: PickerAccount[];
  selectedAccountId?: string;
  equity: number;
  pnl24h: Pnl;
  has24hData?: boolean;
  spark: number[];
  novabotBalance: number;
  user: User;
  counters?: Partial<Record<'webhooks' | 'accounts', number>>;
  noActiveAccounts?: boolean;
  isAdmin?: boolean;
  onSelectAccount?: (id: string) => void;
  onTopUp?: () => void;
  onOpenSettings?: () => void;
  onLogout?: () => void;
};
