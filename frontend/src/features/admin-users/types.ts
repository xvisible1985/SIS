export type UserRole = 'user' | 'admin';
export type UserStatus = 'active' | 'pending' | 'blocked';
export type ExchangeName = 'Bybit' | 'Binance' | 'OKX' | (string & {});
export type AccountPerm = 'read' | 'trade' | 'wd';

export type ApiAccount = {
  id: string;
  exchange: ExchangeName;
  /** имя, заданное пользователем */
  label: string;
  /** API key (полный — компонент сам маскирует) */
  apiKey: string;
  perms: AccountPerm[];
  /** IP, с которого пришёл API */
  ip?: string;
  added: Date;
};

export type AdminUser = {
  id: string;
  name: string;
  email: string;
  role: UserRole;
  curator: boolean;
  status: UserStatus;
  /** баланс NovaBot, USDT */
  balance: number;
  joined: Date;
  lastActive: Date;
  emailVerified: boolean;
  /** id вышестоящего пользователя или null */
  refererId: string | null;
  accounts: ApiAccount[];
  /** причина блокировки, если status === 'blocked' */
  blockReason?: string;
};

export type StatusFilter = 'all' | 'active' | 'pending' | 'blocked' | 'admin' | 'curator';

export type UserListFilters = {
  q: string;
  filter: StatusFilter;
};

/** Действия, которые админ может выполнить — описывают контракт с бэкендом */
export type AdminAction =
  | { type: 'role/set';         userId: string; role: UserRole }
  | { type: 'curator/set';      userId: string; curator: boolean }
  | { type: 'referer/set';      userId: string; refererId: string | null }
  | { type: 'email/verify';     userId: string }
  | { type: 'email/resend';     userId: string }
  | { type: 'email/reset';      userId: string }
  | { type: 'password/set';     userId: string; password: string; requireChange: boolean }
  | { type: 'balance/adjust';   userId: string; amount: number; note?: string }
  | { type: 'block';            userId: string; reason: string }
  | { type: 'unblock';          userId: string }
  | { type: 'account/remove';   userId: string; accountId: string };
