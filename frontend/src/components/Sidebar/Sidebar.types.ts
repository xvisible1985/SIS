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
