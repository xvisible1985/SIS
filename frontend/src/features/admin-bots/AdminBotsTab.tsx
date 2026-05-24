import { useState } from 'react';
import { Plus } from 'lucide-react';
import { useAdminBots } from './api';
import { BotForm } from '../bots/components/BotForm';
import { AdminBotCard } from './AdminBotCard';
import type { Bot as BotType, CreateBotInput } from '../bots/types';

export function AdminBotsTab() {
  const { bots, loading, create, remove, togglePublic, update, refresh } = useAdminBots();
  const [creating, setCreating] = useState(false);
  const [editingBot, setEditingBot] = useState<BotType | null>(null);

  const officialBots = bots.filter(b => b.isOfficial);
  const userBots   = bots.filter(b => !b.isOfficial);

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
          <span className="text-[11px] text-slate-400">{officialBots.length} NovaBot · {userBots.length} пользовательских</span>
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
        <BotForm
          mode="admin"
          onSubmit={handleCreate}
          onClose={() => setCreating(false)}
        />
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
      <div className="flex-1 overflow-auto px-5 py-3">
        {loading ? (
          <div className="py-12 text-center text-sm text-slate-500">Загрузка…</div>
        ) : bots.length === 0 ? (
          <div className="py-12 text-center text-sm text-slate-500">Нет ботов</div>
        ) : (
          <div className="grid grid-cols-1 gap-3.5 sm:grid-cols-2 xl:grid-cols-3">
            {bots.map(bot => (
              <AdminBotCard
                key={bot.id}
                bot={bot}
                onEdit={bot.isOfficial ? () => setEditingBot(bot) : undefined}
                onTogglePublic={() => togglePublic(bot.id, !bot.isPublic)}
                onDelete={() => { if (window.confirm('Удалить бота?')) remove(bot.id); }}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
