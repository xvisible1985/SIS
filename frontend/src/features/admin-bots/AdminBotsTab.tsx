import { useState } from 'react';
import { Plus } from 'lucide-react';
import { useAdminBots } from './api';
import { BotForm } from '../bots/components/BotForm';
import { AdminBotCard } from './AdminBotCard';
import type { Bot as BotType, CreateBotInput } from '../bots/types';

export function AdminBotsTab() {
  const { bots, loading, create, remove, togglePublic, update, approve, reject, refresh } = useAdminBots();
  const [creating, setCreating] = useState(false);
  const [editingBot, setEditingBot] = useState<BotType | null>(null);

  const pendingBots  = bots.filter(b => b.approvalStatus === 'pending');
  const officialBots = bots.filter(b => b.isOfficial && b.approvalStatus !== 'pending');
  const userBots     = bots.filter(b => !b.isOfficial && b.approvalStatus !== 'pending');

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
            {officialBots.length} NovaBot · {userBots.length} пользовательских
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

      {/* Create / Edit modal */}
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

      {/* Cards grid */}
      <div className="flex-1 overflow-auto px-5 py-3 space-y-6">
        {loading ? (
          <div className="py-12 text-center text-sm text-slate-500">Загрузка…</div>
        ) : (
          <>
            {/* ── Pending approval section ────────────────────────────────── */}
            {pendingBots.length > 0 && (
              <div>
                <h3 className="mb-3 text-xs font-bold uppercase tracking-wider text-amber-400">
                  На согласование ({pendingBots.length})
                </h3>
                <div className="grid grid-cols-1 gap-3.5 sm:grid-cols-2 xl:grid-cols-3">
                  {pendingBots.map(bot => (
                    <AdminBotCard
                      key={bot.id}
                      bot={bot}
                      onApprove={() => approve(bot.id)}
                      onReject={() => { if (window.confirm(`Отклонить заявку бота «${bot.name}»?`)) reject(bot.id); }}
                      onDelete={() => { if (window.confirm('Удалить бота?')) remove(bot.id); }}
                    />
                  ))}
                </div>
              </div>
            )}

            {/* ── All other bots ──────────────────────────────────────────── */}
            {(officialBots.length > 0 || userBots.length > 0) && (
              <div>
                {pendingBots.length > 0 && (
                  <h3 className="mb-3 text-xs font-bold uppercase tracking-wider text-slate-400">
                    Все боты
                  </h3>
                )}
                <div className="grid grid-cols-1 gap-3.5 sm:grid-cols-2 xl:grid-cols-3">
                  {[...officialBots, ...userBots].map(bot => (
                    <AdminBotCard
                      key={bot.id}
                      bot={bot}
                      onEdit={bot.isOfficial ? () => setEditingBot(bot) : undefined}
                      onTogglePublic={() => togglePublic(bot.id, !bot.isPublic)}
                      onDelete={() => { if (window.confirm('Удалить бота?')) remove(bot.id); }}
                    />
                  ))}
                </div>
              </div>
            )}

            {bots.length === 0 && (
              <div className="py-12 text-center text-sm text-slate-500">Нет ботов</div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
