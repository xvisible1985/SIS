import { useState } from 'react';
import { Plus, TrendingUp, Search, Shield, Layers, CheckCircle2, Archive, Clock } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { useAdminBots } from './api';
import { BotForm } from '../bots/components/BotForm';
import { AdminBotCard } from './AdminBotCard';
import { PublishToCatalogModal } from './PublishToCatalogModal';
import { getBotKindMeta } from '../bots/botKindMeta';
import type { Bot as BotType, BotKind, CreateBotInput } from '../bots/types';

const KIND_ICONS: Record<BotKind, LucideIcon> = {
  signal: TrendingUp,
  parser: Search,
  hedge:  Shield,
  matrix: Layers,
};

const KIND_GROUPS: { kind: BotKind; label: string }[] = [
  { kind: 'signal', label: 'Signal Bots' },
  { kind: 'parser', label: 'Parser Bots' },
  { kind: 'hedge',  label: 'Hedge Bots'  },
];

export function AdminBotsTab() {
  const { bots, loading, create, togglePublic, update, approve, reject, publishToCatalog, adminDelete } = useAdminBots();
  const [creating, setCreating]     = useState(false);
  const [editingBot, setEditingBot] = useState<BotType | null>(null);
  const [publishBot, setPublishBot] = useState<BotType | null>(null);

  // ── три группы ────────────────────────────────────────────────────────────
  const pendingBots  = bots.filter(b => b.approvalStatus === 'pending');
  const activeBots   = bots.filter(b => b.isPublic  && b.approvalStatus !== 'pending');
  const archivedBots = bots.filter(b => !b.isPublic && b.approvalStatus !== 'pending');

  const getByKind = (list: BotType[], kind: BotKind) =>
    list.filter(b => (b.strategyConfig?.bot_kind ?? 'signal') === kind);

  async function handleCreate(data: CreateBotInput) {
    await create(data);
    setCreating(false);
  }

  async function handleEdit(botId: string, data: CreateBotInput) {
    await update(botId, data);
    setEditingBot(null);
  }

  return (
    <div className="flex h-full flex-col overflow-hidden">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-white/[.06] px-5 py-3">
        <div className="flex items-baseline gap-3">
          <h2 className="m-0 text-sm font-semibold text-slate-100">Библиотека ботов</h2>
          <span className="text-[11px] text-slate-400">
            {activeBots.length} рабочих · {archivedBots.length} архивных
            {pendingBots.length > 0 && (
              <span className="ml-2 rounded-full bg-amber-400/20 px-1.5 py-0.5 text-[10px] font-bold text-amber-300">
                {pendingBots.length} на модерации
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
      {publishBot && (
        <PublishToCatalogModal
          bot={publishBot}
          onClose={() => setPublishBot(null)}
          onPublish={(name, isOfficial, price) => publishToCatalog(publishBot.id, { name, isOfficial, price })}
        />
      )}

      {/* Контент */}
      <div className="flex-1 overflow-auto px-5 py-4 space-y-8">
        {loading ? (
          <div className="py-12 text-center text-sm text-slate-500">Загрузка…</div>
        ) : bots.length === 0 ? (
          <div className="py-12 text-center text-sm text-slate-500">Нет ботов</div>
        ) : (
          <>

            {/* ── На модерации ──────────────────────────────────────────── */}
            {pendingBots.length > 0 && (
              <AreaSection
                label="На модерации"
                count={pendingBots.length}
                icon={<Clock size={14} strokeWidth={2} className="text-amber-400" />}
                labelColor="text-amber-400"
                borderColor="border-amber-400/20"
                bgColor="bg-amber-400/[.04]"
                hint="Боты от пользователей, ожидающие проверки"
              >
                <BotGrid>
                  {pendingBots.map(bot => (
                    <AdminBotCard
                      key={bot.id}
                      bot={bot}
                      onApprove={() => approve(bot.id)}
                      onReject={() => {
                        if (window.confirm(`Отклонить заявку бота «${bot.name}»?`)) reject(bot.id);
                      }}
                      onDelete={() => {
                        if (window.confirm('Удалить бота?')) adminDelete(bot.id);
                      }}
                    />
                  ))}
                </BotGrid>
              </AreaSection>
            )}

            {/* ── Рабочие боты ──────────────────────────────────────────── */}
            <AreaSection
              label="Рабочие боты"
              count={activeBots.length}
              icon={<CheckCircle2 size={14} strokeWidth={2} className="text-emerald-400" />}
              labelColor="text-emerald-400"
              borderColor="border-emerald-400/15"
              bgColor="bg-emerald-400/[.03]"
              hint="Опубликованы в каталоге и доступны пользователям"
            >
              {activeBots.length === 0 ? (
                <EmptyHint>Нет опубликованных ботов</EmptyHint>
              ) : (
                KIND_GROUPS.map(({ kind, label }) => {
                  const group = getByKind(activeBots, kind);
                  if (group.length === 0) return null;
                  const km   = getBotKindMeta(kind);
                  const Icon = KIND_ICONS[kind];
                  return (
                    <KindSubSection
                      key={kind}
                      label={label}
                      count={group.length}
                      icon={<Icon size={12} strokeWidth={2} style={{ color: km.color }} />}
                      accentColor={km.color}
                    >
                      {group.map(bot => (
                        <AdminBotCard
                          key={bot.id}
                          bot={bot}
                          onEdit={() => setEditingBot(bot)}
                          onTogglePublic={() => togglePublic(bot.id, !bot.isPublic)}
                          onPublishToLibrary={() => setPublishBot(bot)}
                          onDelete={() => {
                            if (window.confirm(`Удалить бота «${bot.name}»?`)) adminDelete(bot.id);
                          }}
                        />
                      ))}
                    </KindSubSection>
                  );
                })
              )}
            </AreaSection>

            {/* ── Архивные боты ─────────────────────────────────────────── */}
            <AreaSection
              label="Архивные боты"
              count={archivedBots.length}
              icon={<Archive size={14} strokeWidth={2} className="text-slate-400" />}
              labelColor="text-slate-400"
              borderColor="border-white/[.06]"
              bgColor="bg-white/[.015]"
              hint="Не опубликованы — черновики или снятые с публикации"
            >
              {archivedBots.length === 0 ? (
                <EmptyHint>Нет архивных ботов</EmptyHint>
              ) : (
                KIND_GROUPS.map(({ kind, label }) => {
                  const group = getByKind(archivedBots, kind);
                  if (group.length === 0) return null;
                  const km   = getBotKindMeta(kind);
                  const Icon = KIND_ICONS[kind];
                  return (
                    <KindSubSection
                      key={kind}
                      label={label}
                      count={group.length}
                      icon={<Icon size={12} strokeWidth={2} style={{ color: km.color }} />}
                      accentColor={km.color}
                    >
                      {group.map(bot => (
                        <AdminBotCard
                          key={bot.id}
                          bot={bot}
                          onEdit={() => setEditingBot(bot)}
                          onTogglePublic={() => togglePublic(bot.id, !bot.isPublic)}
                          onPublishToLibrary={() => setPublishBot(bot)}
                          onDelete={() => {
                            if (window.confirm(`Удалить бота «${bot.name}»?`)) adminDelete(bot.id);
                          }}
                        />
                      ))}
                    </KindSubSection>
                  );
                })
              )}
            </AreaSection>

          </>
        )}
      </div>
    </div>
  );
}

/* ── Цветная область (рабочие / архивные / модерация) ───────────────────── */
function AreaSection({
  label, count, icon, labelColor, borderColor, bgColor, hint, children,
}: {
  label: string;
  count: number;
  icon: React.ReactNode;
  labelColor: string;
  borderColor: string;
  bgColor: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={`rounded-[14px] border ${borderColor} ${bgColor} p-4`}>
      {/* секция-заголовок */}
      <div className="mb-4 flex items-center gap-2">
        {icon}
        <h3 className={`text-xs font-bold uppercase tracking-wider ${labelColor}`}>{label}</h3>
        <span className={`rounded-full px-1.5 py-0.5 text-[10px] font-bold ${labelColor} bg-white/[.06]`}>
          {count}
        </span>
        {hint && (
          <span className="ml-1 text-[11px] text-slate-500">{hint}</span>
        )}
      </div>
      <div className="space-y-4">{children}</div>
    </div>
  );
}

/* ── Под-секция по типу бота ─────────────────────────────────────────────── */
function KindSubSection({
  label, count, icon, accentColor, children,
}: {
  label: string;
  count: number;
  icon: React.ReactNode;
  accentColor: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-2.5 flex items-center gap-1.5">
        {icon}
        <span className="text-[11px] font-semibold text-slate-300">{label}</span>
        <span
          className="rounded-full px-1.5 py-0.5 text-[9px] font-bold"
          style={{ background: `${accentColor}22`, color: accentColor }}
        >
          {count}
        </span>
      </div>
      <BotGrid>{children}</BotGrid>
    </div>
  );
}

function BotGrid({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(380px,1fr))] gap-3">
      {children}
    </div>
  );
}

function EmptyHint({ children }: { children: React.ReactNode }) {
  return (
    <p className="py-3 text-[12px] text-slate-600">{children}</p>
  );
}
