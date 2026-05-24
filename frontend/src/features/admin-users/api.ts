import { useState, useEffect, useCallback } from 'react';
import { apiClient } from '../../api/client';
import type { AdminAction, AdminUser, ApiAccount, NovaBotTransaction } from './types';

type RawAccount = Record<string, unknown>;
type RawUser   = Record<string, unknown>;

function parseAccount(raw: RawAccount): ApiAccount {
  return {
    id:       raw.id       as string,
    exchange: raw.exchange as string,
    label:    raw.label    as string,
    apiKey:   raw.apiKey   as string,
    perms:    (raw.perms ?? []) as ApiAccount['perms'],
    ip:       raw.ip as string | undefined,
    added:    new Date(raw.added as string),
  };
}

function parseUser(raw: RawUser): AdminUser {
  const joined = new Date(raw.joined as string);
  return {
    id:            raw.id            as string,
    name:          raw.name          as string,
    email:         raw.email         as string,
    role:          raw.role          as AdminUser['role'],
    curator:       raw.curator       as boolean,
    status:        raw.status        as AdminUser['status'],
    balance:       raw.balance       as number,
    joined,
    lastActive:    raw.lastActive ? new Date(raw.lastActive as string) : joined,
    emailVerified: raw.emailVerified as boolean,
    refererId:     raw.refererId     as string | null,
    accounts:      ((raw.accounts ?? []) as RawAccount[]).map(parseAccount),
    blockReason:   raw.blockReason   as string | undefined,
  };
}

function parseTx(raw: Record<string, unknown>): NovaBotTransaction {
  return {
    id:        raw.id        as string,
    adminId:   raw.adminId   as string,
    amount:    raw.amount    as number,
    note:      raw.note      as string,
    createdAt: new Date(raw.createdAt as string),
  };
}

export function useAdminUsers() {
  const [users, setUsers]     = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await apiClient.get<RawUser[]>('/admin/users');
      setUsers(res.data.map(parseUser));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const action = useCallback(async (a: AdminAction) => {
    switch (a.type) {
      case 'role/set':
        await apiClient.patch(`/admin/users/${a.userId}`, { role: a.role }); break;
      case 'curator/set':
        await apiClient.patch(`/admin/users/${a.userId}`, { curator: a.curator }); break;
      case 'referer/set':
        await apiClient.patch(`/admin/users/${a.userId}`, { refererId: a.refererId }); break;
      case 'email/verify':
        await apiClient.post(`/admin/users/${a.userId}/email/verify`); break;
      case 'email/resend':
        await apiClient.post(`/admin/users/${a.userId}/email/resend`); break;
      case 'email/reset':
        await apiClient.post(`/admin/users/${a.userId}/email/reset`); break;
      case 'password/set':
        await apiClient.post(`/admin/users/${a.userId}/password`, {
          password: a.password, requireChange: a.requireChange,
        }); break;
      case 'balance/adjust':
        await apiClient.post(`/admin/users/${a.userId}/balance/adjust`, {
          amount: a.amount, note: a.note,
        }); break;
      case 'block':
        await apiClient.post(`/admin/users/${a.userId}/block`, { reason: a.reason }); break;
      case 'unblock':
        await apiClient.post(`/admin/users/${a.userId}/unblock`); break;
      case 'account/remove':
        await apiClient.delete(`/admin/users/${a.userId}/accounts/${a.accountId}`); break;
      case 'account/sign-agreement':
        await apiClient.post(`/admin/accounts/${a.accountId}/sign-agreement`, {
          categoryV2: a.categoryV2,
        }); break;
    }
    await load();
  }, [load]);

  const fetchTransactions = useCallback(async (
    userId: string,
    params?: { limit?: number; offset?: number; type?: 'all' | 'credit' | 'debit' },
  ) => {
    const p = new URLSearchParams();
    if (params?.limit)  p.set('limit', String(params.limit));
    if (params?.offset) p.set('offset', String(params.offset));
    if (params?.type && params.type !== 'all') p.set('type', params.type);
    const qs = p.toString();
    const res = await apiClient.get<Record<string, unknown>[]>(
      `/admin/users/${userId}/transactions${qs ? '?' + qs : ''}`,
    );
    return res.data.map(parseTx);
  }, []);

  return { users, loading, action, refresh: load, fetchTransactions };
}
