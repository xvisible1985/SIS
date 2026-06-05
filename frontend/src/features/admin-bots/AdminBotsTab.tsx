import { useState } from 'react';
import { Plus, TrendingUp, Search, Shield } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { useAdminBots } from './api';
import { BotForm } from '../bots/components/BotForm';
import { AdminBotCard } from './AdminBotCard';
import { getBotKindMeta } from '../bots/botKindMeta';
import type { Bot as BotType, BotKind, CreateBotInput } from '../bots/types';

const KIND_ICONS: Record<BotKind, LucideIcon> = {
  signal: TrendingUp,
  parser: Search,
  hedge:  Shield,
};

const KIND_GROUPS: { kind: BotKind; label: string }[] = [
  { kind: 'signal', label: 'Signal Bots' },
  { kind: 'parser', label: 'Parser Bots' },
  { kind: 'hedge',  label: 'Hedge Bots'  },
];

export function AdminBotsTab() {
  const { bots, loading, create, remove, togglePublic, update, approve, reject } = useAdminBots();
  const [creating, setCreating]     = useState(false);
  const [editingBot, setEditingBot] = useState<BotType | null>(null);

  const pendingBots  = bots.filter(b => b.approvalStatus === 'pending');
  const restBots     = bots.filter(b => b.approvalStatus !== 'pending');

  const getBotsByKind = (kind: BotKind) =>
    restBots.filter(b => (b.strategyConfig?.bot_kind ?? 'signal') === kind);

  async function handleCreate(data: CreateBotInput) {
    await create(data);
    setCreating(false);
  }

  async function handleEdit(botId: string, data: CreateBotInput) {
    await update(botId, data);
    setEditingBot(null);
  }

  const officialCount = restBots.filter(b => b.isOfficial).length;
  const userCount     = restBots.filter(b => !b.isOfficial).length;

  return (
    <div className="flex h-full flex-col overflow-hidden">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-white/[.06] px-5 py-3">
        <div className="flex items-baseline gap-3">
          <h2 className="m-0 text-sm font-semibold text-slate-100">Библиотека ботов</h2>
          <span className="text-[11px] text-slate-400">
            {officialCount} NovaBot · {userCount} пользовательских
            {pendingBots.length > 0 && (
              <span className="ml-2 rounded-full bg-amber-400/20 px-1.5 py-0.5 text-[10px] font-bold text-amber-300">
                {pendingBots.length} на согласовании
              </span>
            )}
          </span>
        </div>
        <button
          type="button"
          onClick={() => setCreating(true)}
          className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] px-3 py-1.5 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(74,125,255,.6)]"
        >
          <Plus size={12} strokeWidth={2.4} />
          Новый бот NovaBot
        </button>
      </div>

      {/* Модалки */}
      {creating && (
        <BotForm mode="admin" onSubmit={handleCreate} onClose={() => setCreating(false)} />
      )}
      {editingBot && (
        <BotForm
          mode="admin"
          bot={editingBot}
          onSubmit={(data) => handleEdit(editingBot.id, data)}
          onClose={() => setEditingBot(null)}
        />
      )}

      {/* Контент */}
      <div className="flex-1 overflow-auto px-5 py-4 space-y-7">
        {loading ? (
          <div className="py-12 text-center text-sm text-slate-500">Загрузка…</div>
        ) : bots.length === 0 ? (
          <div className="py-12 text-center text-sm text-slate-500">Нет ботов</div>
        ) : (
          <>
            {/* ── На согласовании ──────────────────────────────────────── */}
            {pendingBots.length > 0 && (
              <Section
                label={`На согласовании (${pendingBots.length})`}
                labelColor="text-amber-400"
              >
                {pendingBots.map(bot => (
                  <AdminBotCard
                    key={bot.id}
                    bot={bot}
                    onApprove={() => approve(bot.id)}
                    onReject={() => {
                      if (window.confirm(`Отклонить заявку бота «${bot.name}»?`)) reject(bot.id);
                    }}
                    onDelete={() => {
                      if (window.confirm('Удалить бота?')) remove(bot.id);
                    }}
                  />
                ))}
              </Section>
            )}

            {/* ── Группы по типу бота ──────────────────────────────────── */}
            {KIND_GROUPS.map(({ kind, label }) => {
              const group = getBotsByKind(kind);
              if (group.length === 0) return null;
              const km   = getBotKindMeta(kind);
              const Icon = KIND_ICONS[kind];
              return (
                <Section
                  key={kind}
                  label={label}
                  count={group.length}
                  icon={<Icon size={13} strokeWidth={2} style={{ color: km.color }} />}
                  labelColor="text-slate-200"
                  accentColor={km.color}
                >
                  {group.map(bot => (
                    <AdminBotCard
                      key={bot.id}
                      bot={bot}
                      onEdit={bot.isOfficial ? () => setEditingBot(bot) : undefined}
                      onTogglePublic={() => togglePublic(bot.id, !bot.isPublic)}
                      onDelete={() => {
                        if (window.confirm('Удалить бота?')) remove(bot.id);
                      }}
                    />
                  ))}
                </Section>
              );
            })}
          </>
        )}
      </div>
    </div>
  );
}

/* ── Section header + grid ────────────────────────────────────────────── */
function Section({
  label,
  count,
  icon,
  labelColor,
  accentColor,
  children,
}: {
  label: string;
  count?: number;
  icon?: React.ReactNode;
  labelColor: string;
  accentColor?: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-3 flex items-center gap-2">
        {icon}
        <h3 className={`text-xs font-bold uppercase tracking-wider ${labelColor}`}>
          {label}
        </h3>
        {count !== undefined && accentColor && (
          <span
            className="rounded-full px-1.5 py-0.5 text-[10px] font-bold"
            style={{
              background: `${accentColor}22`,
              color:      accentColor,
            }}
          >
            {count}
          </span>
        )}
      </div>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(420px,1fr))] gap-3.5">
        {children}
      </div>
    </div>
  );
}
