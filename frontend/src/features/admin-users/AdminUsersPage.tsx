import { useState } from 'react';
import type { AdminAction, AdminUser, NovaBotTransaction } from './types';
import { UserList } from './components/UserList';
import { UserDetailPanel } from './components/UserDetailPanel';

type Props = {
  users: AdminUser[];
  /** Реализуй через axios — см. README, секцию «API контракт» */
  onAction: (action: AdminAction) => Promise<void> | void;
  /** Опционально — клик по «Создать пользователя» */
  onCreateUser?: () => void;
  /** Опционально — клик по «Обновить» (обычно refetch) */
  onRefresh?: () => void;
  /** Загрузка истории транзакций NovaBot */
  fetchTransactions?: (
    userId: string,
    params?: { limit?: number; offset?: number; type?: 'all' | 'credit' | 'debit' },
  ) => Promise<NovaBotTransaction[]>;
};

export function AdminUsersPage({ users, onAction, onCreateUser, onRefresh, fetchTransactions }: Props) {
  const [selectedId, setSelectedId] = useState<string | null>(users[0]?.id ?? null);
  const selected = users.find((u) => u.id === selectedId);

  return (
    <div className="flex h-full min-h-screen bg-[#0a0d14] text-slate-200">
      <UserList
        users={users}
        selectedId={selectedId}
        onSelect={setSelectedId}
        onCreate={onCreateUser}
        onRefresh={onRefresh}
      />
      {selected && (
        <UserDetailPanel
          user={selected}
          users={users}
          onClose={() => setSelectedId(null)}
          onAction={onAction}
          fetchTransactions={fetchTransactions}
        />
      )}
    </div>
  );
}
