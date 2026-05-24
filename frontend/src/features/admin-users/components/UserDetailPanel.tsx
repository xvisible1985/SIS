import { useCallback, useEffect, useState } from 'react';
import {
  X, User, Shield, Star, Mail, CheckCircle2, Check, Lock,
  Plus, Minus, Ban, ArrowUpRight, MoreHorizontal, RefreshCw,
  // FileSignature,
} from 'lucide-react';
import type { AdminAction, AdminUser, NovaBotTransaction } from '../types';
import { exchangeStyle, fmtDate, fmtDateTime, fmtMoney, generatePassword } from '../utils';
import { Avatar } from './Avatar';
import { StatusPill } from './StatusPill';
import { Segmented } from './Segmented';
import { Toggle } from './Toggle';
import { MaskedKey } from './MaskedKey';
import { RefererPicker } from './RefererPicker';

type Props = {
  user: AdminUser;
  users: AdminUser[];
  onClose: () => void;
  /** Дать наружу действия — реализуй через axios/mutation */
  onAction: (action: AdminAction) => Promise<void> | void;
  /** Загрузить историю транзакций NovaBot */
  fetchTransactions?: (
    userId: string,
    params?: { limit?: number; offset?: number; type?: 'all' | 'credit' | 'debit' },
  ) => Promise<NovaBotTransaction[]>;
};

/** Draft holds the editable subset that survives until "Save" — role/curator/refererId */
type Draft = Pick<AdminUser, 'role' | 'curator' | 'refererId'>;

export function UserDetailPanel({ user, users, onClose, onAction, fetchTransactions }: Props) {
  const initialDraft: Draft = { role: user.role, curator: user.curator, refererId: user.refererId };
  const [draft, setDraft] = useState<Draft>(initialDraft);

  // local form state for action sections
  const [bonus, setBonus] = useState('');
  const [bonusNote, setBonusNote] = useState('');
  const [newPw, setNewPw] = useState('');
  const [requirePwChange, setRequirePwChange] = useState(true);

  // transaction history
  const [txs, setTxs] = useState<NovaBotTransaction[]>([]);
  const [txLoading, setTxLoading] = useState(false);
  const [txFilter, setTxFilter] = useState<'all' | 'credit' | 'debit'>('all');
  const [txOffset, setTxOffset] = useState(0);
  const TX_PAGE = 20;

  const loadTxs = useCallback(async () => {
    if (!fetchTransactions) return;
    setTxLoading(true);
    try {
      const data = await fetchTransactions(user.id, {
        limit: TX_PAGE,
        offset: txOffset,
        type: txFilter,
      });
      setTxs(data);
    } catch {
      setTxs([]);
    } finally {
      setTxLoading(false);
    }
  }, [fetchTransactions, user.id, txFilter, txOffset]);

  useEffect(() => {
    setDraft({ role: user.role, curator: user.curator, refererId: user.refererId });
    setBonus(''); setBonusNote(''); setNewPw('');
  }, [user.id, user.role, user.curator, user.refererId]);

  useEffect(() => {
    setTxOffset(0);
  }, [user.id, txFilter]);

  useEffect(() => {
    loadTxs();
  }, [loadTxs]);

  const dirty = JSON.stringify(draft) !== JSON.stringify(initialDraft);

  const refCount = users.filter((u) => u.refererId === user.id).length;

  const saveProfile = async () => {
    if (draft.role !== user.role)
      await onAction({ type: 'role/set', userId: user.id, role: draft.role });
    if (draft.curator !== user.curator)
      await onAction({ type: 'curator/set', userId: user.id, curator: draft.curator });
    if (draft.refererId !== user.refererId)
      await onAction({ type: 'referer/set', userId: user.id, refererId: draft.refererId });
  };

  const bonusNum = parseFloat(bonus);
  const validBonus = !isNaN(bonusNum) && bonusNum !== 0;

  return (
    <aside className="flex h-full w-[520px] shrink-0 flex-col overflow-auto border-l border-white/[.06] bg-[#0c1018] pb-20">
      {/* HEADER */}
      <div className="border-b border-white/[.05] px-5 pb-4 pt-4">
        <div className="flex items-start gap-3.5">
          <Avatar name={user.name} size={56} status={user.status} />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h2 className="m-0 font-display text-xl font-bold tracking-tight text-slate-50">{user.name}</h2>
              <StatusPill status={user.status} />
            </div>
            <div className="mt-0.5 font-mono text-xs text-slate-300">
              {user.email}
              {user.emailVerified ? (
                <span className="ml-2 inline-flex items-center gap-1 text-emerald-300">
                  <CheckCircle2 size={11} /> подтверждён
                </span>
              ) : (
                <span className="ml-2 text-amber-400">не подтверждён</span>
              )}
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="flex h-7 w-7 items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:text-slate-100"
          >
            <X size={14} />
          </button>
        </div>

        <div className="mt-4 grid grid-cols-3 gap-2">
          <StatBox label="Баланс NovaBot" value={fmtMoney(user.balance)} c="text-emerald-300" />
          <StatBox label="API ключи"      value={user.accounts.length} />
          <StatBox label="Рефералов"      value={refCount} />
        </div>
      </div>

      {/* TYPE & PERMISSIONS */}
      <Section title="Роль и доступ" hint="права в системе">
        <div className="grid gap-3.5">
          <div>
            <Label>Тип пользователя</Label>
            <Segmented
              value={draft.role}
              onChange={(v) => setDraft({ ...draft, role: v })}
              options={[
                { value: 'user', label: 'Пользователь', icon: User },
                {
                  value: 'admin', label: 'Администратор', icon: Shield,
                  activeClass: 'border-[#5b8cff]/32 bg-[#5b8cff]/[.18] text-[#b8c8ff]',
                },
              ]}
            />
          </div>

          <div className="flex items-center justify-between rounded-[10px] border border-[#c14dff]/20 bg-[#c14dff]/[.06] p-3">
            <div className="flex items-center gap-2.5">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg border border-[#c14dff]/30 bg-[#c14dff]/[.18] text-[#d8a4ff]">
                <Star size={16} strokeWidth={2} />
              </div>
              <div>
                <div className="text-sm font-semibold text-slate-50">Куратор</div>
                <div className="mt-0.5 text-[11px] text-slate-300">Доступ к реферальной сети и доходам команды</div>
              </div>
            </div>
            <Toggle
              on={draft.curator}
              onChange={(v) => setDraft({ ...draft, curator: v })}
              label="Куратор"
            />
          </div>
        </div>
      </Section>

      {/* REFERRER */}
      <Section title="Реферер" hint="вышестоящий пользователь">
        <Label>Привязать как реферала к</Label>
        <RefererPicker
          value={draft.refererId}
          onChange={(v) => setDraft({ ...draft, refererId: v })}
          currentUserId={user.id}
          users={users}
        />
        {draft.refererId && (
          <div className="mt-2 flex items-center gap-1.5 rounded-md bg-black/20 px-2.5 py-2 text-[11px] text-slate-400">
            <ArrowUpRight size={11} />
            <span>
              Доход с этого пользователя идёт{' '}
              <span className="font-semibold text-[#b8c8ff]">
                {users.find((u) => u.id === draft.refererId)?.name}
              </span>
            </span>
          </div>
        )}
      </Section>

      {/* EMAIL */}
      <Section title="Подтверждение email">
        {user.emailVerified ? (
          <div className="flex items-center gap-2.5 rounded-lg border border-emerald-400/22 bg-emerald-400/[.08] px-3 py-2.5">
            <CheckCircle2 size={16} className="text-emerald-300" />
            <div className="flex-1">
              <div className="text-xs font-semibold text-emerald-300">Email подтверждён</div>
              <div className="mt-0.5 text-[11px] text-slate-400">{user.email}</div>
            </div>
            <BtnGhost onClick={() => onAction({ type: 'email/reset', userId: user.id })}>Сбросить</BtnGhost>
          </div>
        ) : (
          <div className="flex items-center gap-2.5 rounded-lg border border-amber-500/22 bg-amber-500/[.08] px-3 py-2.5">
            <Mail size={16} className="text-amber-400" />
            <div className="flex-1">
              <div className="text-xs font-semibold text-amber-400">Email не подтверждён</div>
              <div className="mt-0.5 text-[11px] text-slate-400">Подтвердить вручную или отправить письмо ещё раз</div>
            </div>
            <BtnGhost onClick={() => onAction({ type: 'email/resend', userId: user.id })}>Отправить</BtnGhost>
            <BtnSuccess onClick={() => onAction({ type: 'email/verify', userId: user.id })}>
              <Check size={12} strokeWidth={2.4} />Подтвердить
            </BtnSuccess>
          </div>
        )}
      </Section>

      {/* API ACCOUNTS */}
      <Section
        title={`API-аккаунты · ${user.accounts.length}`}
        right={
          <BtnGhost>
            <Plus size={11} strokeWidth={2.4} />Добавить
          </BtnGhost>
        }
      >
        {user.accounts.length === 0 ? (
          <div className="rounded-lg bg-black/20 px-3 py-4 text-center text-xs text-slate-400">
            У пользователя нет подключённых аккаунтов
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            {user.accounts.map((acc) => {
              const ex = exchangeStyle(acc.exchange);
              return (
                <div
                  key={acc.id}
                  className="grid grid-cols-[auto_1fr_auto] items-center gap-3 rounded-[10px] border border-white/[.06] bg-white/[.02] px-3 py-2.5"
                >
                  <div
                    className="flex h-8 w-8 items-center justify-center rounded-[7px] font-mono text-[10px] font-extrabold tracking-wide"
                    style={{ background: ex.bg, color: ex.fg }}
                  >
                    {ex.abbr}
                  </div>
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-semibold text-slate-50">{acc.label}</span>
                      <span className="text-[10px] text-slate-400">·</span>
                      <span className="text-[11px] text-slate-300">{acc.exchange}</span>
                    </div>
                    <div className="mt-1">
                      <MaskedKey value={acc.apiKey} />
                    </div>
                    <div className="mt-1 flex items-center gap-2 text-[10px] text-slate-400">
                      <span className="flex gap-1">
                        {acc.perms.map((p) => (
                          <span
                            key={p}
                            className={
                              'rounded-[3px] border px-1.5 py-px text-[9px] font-bold uppercase tracking-wider ' +
                              (p === 'wd'
                                ? 'border-rose-400/25 bg-rose-400/[.12] text-rose-300'
                                : 'border-[#5b8cff]/25 bg-[#5b8cff]/[.12] text-[#b8c8ff]')
                            }
                          >
                            {p}
                          </span>
                        ))}
                      </span>
                      {acc.ip && (
                        <>
                          <span className="h-[2px] w-[2px] rounded-full bg-slate-600" />
                          <span className="font-mono">IP {acc.ip}</span>
                        </>
                      )}
                      <span className="h-[2px] w-[2px] rounded-full bg-slate-600" />
                      <span>с {fmtDate(acc.added)}</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-1">
                    { /*
                    <button
                      type="button"
                      className="flex h-[30px] w-[30px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:text-emerald-300 hover:border-emerald-400/30 hover:bg-emerald-400/[.08]"
                      title="Подписать торговое соглашение (Bybit)"
                      onClick={() => onAction({ type: 'account/sign-agreement', userId: user.id, accountId: acc.id })}
                    >
                      <FileSignature size={14} />
                    </button>
                    */ }
                    <button
                      type="button"
                      className="flex h-[30px] w-[30px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:text-rose-300 hover:border-rose-400/30 hover:bg-rose-400/[.08]"
                      title="Удалить аккаунт"
                      onClick={() => {
                        if (window.confirm(`Удалить аккаунт «${acc.label}»?`)) {
                          onAction({ type: 'account/remove', userId: user.id, accountId: acc.id });
                        }
                      }}
                    >
                      <X size={14} />
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Section>

      {/* PASSWORD */}
      <Section title="Сброс пароля">
        <Label>Новый пароль</Label>
        <div className="mb-2.5 flex gap-2">
          <input
            type="text"
            value={newPw}
            onChange={(e) => setNewPw(e.target.value)}
            placeholder="введите вручную или сгенерируйте"
            className="flex-1 rounded-lg border border-white/[.08] bg-black/25 px-3 py-2.5 font-mono text-xs text-slate-100 outline-none"
          />
          <BtnGhost onClick={() => setNewPw(generatePassword())}>Сгенерировать</BtnGhost>
        </div>
        <label className="flex cursor-pointer items-center gap-2 text-xs text-slate-300">
          <input
            type="checkbox"
            checked={requirePwChange}
            onChange={(e) => setRequirePwChange(e.target.checked)}
            className="accent-[#5b8cff]"
          />
          Запросить смену при следующем входе
        </label>
        <BtnPrimary
          className="mt-3 w-full"
          disabled={newPw.length < 6}
          onClick={() => onAction({ type: 'password/set', userId: user.id, password: newPw, requireChange: requirePwChange })}
        >
          <Lock size={12} strokeWidth={2} />Применить пароль
        </BtnPrimary>
      </Section>

      {/* BALANCE */}
      <Section title="Баланс NovaBot" hint="ручное начисление / списание">
        <div className="mb-3.5 flex items-center gap-3.5 rounded-[10px] border border-[#5b8cff]/18 bg-[linear-gradient(180deg,rgba(91,140,255,.05),rgba(123,91,255,.04))] px-3.5 py-3.5">
          <div className="flex-1">
            <div className="text-[10px] font-semibold uppercase tracking-wider text-slate-400">Текущий баланс</div>
            <div className="mt-0.5 font-display text-2xl font-bold tracking-tight text-white">{fmtMoney(user.balance)}</div>
          </div>
          {validBonus && (
            <div className="text-right">
              <div className="text-[10px] font-semibold uppercase tracking-wider text-slate-400">Станет</div>
              <div className={'mt-0.5 font-display text-xl font-bold tracking-tight ' + (user.balance + bonusNum >= 0 ? 'text-emerald-300' : 'text-rose-300')}>
                {fmtMoney(user.balance + bonusNum)}
              </div>
            </div>
          )}
        </div>

        <Label>Сумма (USDT)</Label>
        <div className="mb-2.5 flex items-stretch gap-2">
          <button
            type="button"
            onClick={() => setBonus(String((parseFloat(bonus) || 0) - 50))}
            className="flex h-9 w-9 items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:text-slate-100"
          >
            <Minus size={12} strokeWidth={2.4} />
          </button>
          <div className="relative flex-1">
            <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 font-mono text-sm text-slate-400">$</span>
            <input
              type="text"
              inputMode="decimal"
              value={bonus}
              onChange={(e) => setBonus(e.target.value.replace(/[^\-\d.]/g, ''))}
              placeholder="0.00"
              className="w-full rounded-lg border border-white/[.08] bg-black/25 px-3 py-2.5 pl-6 font-mono text-[15px] font-semibold text-slate-50 outline-none"
            />
          </div>
          <button
            type="button"
            onClick={() => setBonus(String((parseFloat(bonus) || 0) + 50))}
            className="flex h-9 w-9 items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:text-slate-100"
          >
            <Plus size={12} strokeWidth={2.4} />
          </button>
        </div>
        <div className="mb-3 flex flex-wrap gap-1.5">
          {[10, 25, 50, 100, 250, 500].map((n) => (
            <button
              key={n}
              type="button"
              onClick={() => setBonus(String(n))}
              className="rounded-md border border-white/[.07] bg-white/[.03] px-2.5 py-1 text-[11px] font-semibold text-slate-200 hover:bg-white/[.06]"
            >
              +${n}
            </button>
          ))}
        </div>

        <Label>Примечание (опц.)</Label>
        <input
          value={bonusNote}
          onChange={(e) => setBonusNote(e.target.value)}
          placeholder="напр. компенсация, бонус, корректировка"
          className="mb-3 w-full rounded-lg border border-white/[.08] bg-black/25 px-3 py-2.5 text-sm text-slate-100 outline-none"
        />

        <div className="flex gap-2">
          <BtnSuccess
            className="flex-1"
            disabled={!validBonus || bonusNum <= 0}
            onClick={() => onAction({ type: 'balance/adjust', userId: user.id, amount: bonusNum, note: bonusNote || undefined })}
          >
            <Plus size={12} strokeWidth={2.4} />Начислить ${validBonus && bonusNum > 0 ? bonusNum : 0}
          </BtnSuccess>
          <BtnDanger
            className="flex-1"
            disabled={!validBonus || bonusNum >= 0}
            onClick={() => onAction({ type: 'balance/adjust', userId: user.id, amount: bonusNum, note: bonusNote || undefined })}
          >
            <Minus size={12} strokeWidth={2.4} />Списать ${validBonus && bonusNum < 0 ? Math.abs(bonusNum) : 0}
          </BtnDanger>
        </div>
      </Section>

      {/* TRANSACTION HISTORY */}
      {fetchTransactions && (
        <Section
          title="История баланса"
          hint={`${txFilter === 'all' ? 'все' : txFilter === 'credit' ? 'начисления' : 'списания'}`}
          right={
            <button
              type="button"
              onClick={loadTxs}
              disabled={txLoading}
              className="flex h-7 w-7 items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:text-slate-100 disabled:opacity-40"
              title="Обновить"
            >
              <RefreshCw size={12} className={txLoading ? 'animate-spin' : ''} />
            </button>
          }
        >
          {/* Filter */}
          <div className="mb-3 flex gap-1.5">
            {([
              { key: 'all', label: 'Все' },
              { key: 'credit', label: 'Начисления' },
              { key: 'debit', label: 'Списания' },
            ] as const).map((f) => (
              <button
                key={f.key}
                type="button"
                onClick={() => setTxFilter(f.key)}
                className={
                  'rounded-md px-2.5 py-1 text-[11px] font-semibold transition-colors ' +
                  (txFilter === f.key
                    ? 'bg-[#5b8cff]/[.18] text-[#b8c8ff]'
                    : 'border border-white/[.06] bg-white/[.02] text-slate-300 hover:bg-white/[.04]')
                }
              >
                {f.label}
              </button>
            ))}
          </div>

          {txLoading ? (
            <div className="py-4 text-center text-xs text-slate-500">Загрузка…</div>
          ) : txs.length === 0 ? (
            <div className="rounded-lg bg-black/20 px-3 py-4 text-center text-xs text-slate-400">
              История операций пуста
            </div>
          ) : (
            <div className="flex flex-col gap-1.5">
              {txs.map((tx) => (
                <div
                  key={tx.id}
                  className="flex items-center gap-3 rounded-lg border border-white/[.04] bg-white/[.015] px-3 py-2"
                >
                  <div
                    className={
                      'flex h-7 w-7 shrink-0 items-center justify-center rounded-md ' +
                      (tx.amount >= 0
                        ? 'border border-emerald-400/20 bg-emerald-400/[.10] text-emerald-300'
                        : 'border border-rose-400/20 bg-rose-400/[.10] text-rose-300')
                    }
                  >
                    {tx.amount >= 0 ? <Plus size={12} strokeWidth={2.4} /> : <Minus size={12} strokeWidth={2.4} />}
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className={`font-mono text-sm font-semibold ${tx.amount >= 0 ? 'text-emerald-300' : 'text-rose-300'}`}>
                        {tx.amount >= 0 ? '+' : ''}{fmtMoney(tx.amount)}
                      </span>
                      {tx.note && (
                        <span className="truncate text-[11px] text-slate-400">· {tx.note}</span>
                      )}
                    </div>
                    <div className="mt-0.5 flex items-center gap-2 text-[10px] text-slate-500">
                      <span>{fmtDateTime(tx.createdAt)}</span>
                      <span className="h-[2px] w-[2px] rounded-full bg-slate-600" />
                      <span className="font-mono">admin {tx.adminId?.slice(0, 8) ?? '—'}</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}

          {/* Pagination */}
          <div className="mt-3 flex items-center justify-between">
            <button
              type="button"
              disabled={txOffset === 0 || txLoading}
              onClick={() => setTxOffset((o) => Math.max(0, o - TX_PAGE))}
              className="rounded-md border border-white/[.06] bg-white/[.02] px-3 py-1.5 text-[11px] font-semibold text-slate-300 hover:bg-white/[.04] disabled:opacity-30"
            >
              ← Назад
            </button>
            <span className="text-[11px] text-slate-500">
              {txOffset + 1}–{txOffset + txs.length}
            </span>
            <button
              type="button"
              disabled={txs.length < TX_PAGE || txLoading}
              onClick={() => setTxOffset((o) => o + TX_PAGE)}
              className="rounded-md border border-white/[.06] bg-white/[.02] px-3 py-1.5 text-[11px] font-semibold text-slate-300 hover:bg-white/[.04] disabled:opacity-30"
            >
              Вперёд →
            </button>
          </div>
        </Section>
      )}

      {/* BLOCK */}
      <Section title="Блокировка">
        {user.status === 'blocked' ? (
          <div className="rounded-[10px] border border-rose-400/22 bg-rose-400/[.08] px-3.5 py-3">
            <div className="mb-1.5 flex items-center gap-2">
              <Ban size={14} className="text-rose-300" />
              <span className="text-xs font-bold uppercase tracking-wider text-rose-300">
                Пользователь заблокирован
              </span>
            </div>
            {user.blockReason && (
              <div className="text-xs leading-relaxed text-slate-200">{user.blockReason}</div>
            )}
            <BtnSuccess className="mt-2.5" onClick={() => onAction({ type: 'unblock', userId: user.id })}>
              <Check size={12} strokeWidth={2.4} />Разблокировать
            </BtnSuccess>
          </div>
        ) : (
          <div>
            <div className="mb-2.5 text-xs leading-relaxed text-slate-400">
              Заблокированный пользователь не сможет войти, его боты и стратегии будут остановлены.
            </div>
            <BtnDanger
              onClick={() => {
                const reason = window.prompt('Причина блокировки?') ?? '';
                if (reason) onAction({ type: 'block', userId: user.id, reason });
              }}
            >
              <Ban size={12} strokeWidth={2} />Заблокировать пользователя
            </BtnDanger>
          </div>
        )}
      </Section>

      {/* Sticky save bar */}
      {dirty && (
        <div className="sticky bottom-0 z-10 flex items-center gap-3 border-t border-[#5b8cff]/25 bg-[#0c1018]/95 px-5 py-3 shadow-[0_-8px_24px_-8px_rgba(0,0,0,.6)] backdrop-blur">
          <span className="text-xs font-semibold text-[#b8c8ff]">Есть несохранённые изменения</span>
          <div className="flex-1" />
          <BtnGhost onClick={() => setDraft(initialDraft)}>Отменить</BtnGhost>
          <BtnPrimary onClick={saveProfile}>
            <Check size={12} strokeWidth={2.4} />Сохранить
          </BtnPrimary>
        </div>
      )}
    </aside>
  );
}

/* --- internal helpers --- */

function Section({
  title, hint, right, children,
}: { title: string; hint?: string; right?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="border-t border-white/[.05] px-5 py-4">
      <div className="mb-3 flex items-center gap-2">
        <h3 className="m-0 text-[11px] font-bold uppercase tracking-wider text-slate-300">{title}</h3>
        {hint && <span className="text-[11px] text-slate-500">· {hint}</span>}
        <div className="flex-1" />
        {right}
      </div>
      {children}
    </div>
  );
}

function Label({ children }: { children: React.ReactNode }) {
  return <label className="mb-1.5 block text-[11px] font-semibold text-slate-400">{children}</label>;
}

function StatBox({ label, value, c = 'text-slate-50' }: { label: string; value: string | number; c?: string }) {
  return (
    <div className="rounded-[10px] border border-white/[.06] bg-white/[.02] px-3 py-2.5">
      <div className="text-[10px] font-bold uppercase tracking-wider text-slate-400">{label}</div>
      <div className={`mt-1 font-display text-lg font-bold tracking-tight ${c}`}>{value}</div>
    </div>
  );
}

type BtnProps = React.ButtonHTMLAttributes<HTMLButtonElement>;

function BtnPrimary({ className = '', children, ...rest }: BtnProps) {
  return (
    <button
      type="button"
      {...rest}
      className={
        'inline-flex items-center justify-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] px-3.5 py-2 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(74,125,255,.6)] disabled:opacity-40 ' +
        className
      }
    >
      {children}
    </button>
  );
}
function BtnGhost({ className = '', children, ...rest }: BtnProps) {
  return (
    <button
      type="button"
      {...rest}
      className={
        'inline-flex items-center justify-center gap-1.5 rounded-lg border border-white/[.08] bg-white/[.03] px-3 py-2 text-xs font-semibold text-slate-200 hover:bg-white/[.06] disabled:opacity-40 ' +
        className
      }
    >
      {children}
    </button>
  );
}
function BtnSuccess({ className = '', children, ...rest }: BtnProps) {
  return (
    <button
      type="button"
      {...rest}
      className={
        'inline-flex items-center justify-center gap-1.5 rounded-lg border border-emerald-400/30 bg-emerald-400/[.12] px-3.5 py-2 text-xs font-semibold text-emerald-300 hover:bg-emerald-400/[.18] disabled:opacity-40 ' +
        className
      }
    >
      {children}
    </button>
  );
}
function BtnDanger({ className = '', children, ...rest }: BtnProps) {
  return (
    <button
      type="button"
      {...rest}
      className={
        'inline-flex items-center justify-center gap-1.5 rounded-lg border border-rose-400/28 bg-rose-400/[.10] px-3.5 py-2 text-xs font-semibold text-rose-300 hover:bg-rose-400/[.18] disabled:opacity-40 ' +
        className
      }
    >
      {children}
    </button>
  );
}
