import { useEffect } from 'react';
import { AlertTriangle } from 'lucide-react';

type Props = {
  tradesTotal: number;
  netPnlTotal: number;
  tradesWin:   number;
  onConfirm:   () => void;
  onCancel:    () => void;
};

/** Подтверждение обнуления статистики сделок при изменении настроек бота */
export function ResetStatsConfirmModal({ tradesTotal, netPnlTotal, tradesWin, onConfirm, onCancel }: Props) {
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel();
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [onCancel]);

  const pnl    = netPnlTotal;
  const pnlPos = pnl >= 0;
  const winRate = tradesTotal > 0 ? Math.round((tradesWin / tradesTotal) * 100) : null;

  return (
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center"
      style={{ background: 'rgba(0,0,0,0.72)', backdropFilter: 'blur(4px)' }}
      onClick={(e) => { if (e.target === e.currentTarget) onCancel(); }}
    >
      <div
        className="relative w-full max-w-[420px] rounded-2xl border p-6"
        style={{
          background:  'linear-gradient(180deg,#10141f 0%,#0c1018 100%)',
          borderColor: 'rgba(255,255,255,0.08)',
          boxShadow:   '0 32px 80px -20px rgba(0,0,0,0.8)',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Иконка + заголовок */}
        <div className="mb-4 flex items-start gap-3">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border border-amber-500/25 bg-amber-500/[.12]">
            <AlertTriangle size={16} className="text-amber-400" strokeWidth={2} />
          </div>
          <div>
            <div className="font-display text-[16px] font-bold tracking-tight text-slate-50">
              Обнуление статистики
            </div>
            <div className="mt-0.5 text-[12px] leading-[1.5] text-slate-400">
              Изменение настроек стратегии удалит всю историю сделок бота.
            </div>
          </div>
        </div>

        {/* Блок статистики */}
        <div
          className="mb-5 grid grid-cols-3 gap-px overflow-hidden rounded-[10px] border"
          style={{ borderColor: 'rgba(255,255,255,0.06)', background: 'rgba(255,255,255,0.06)' }}
        >
          <StatCell label="Сделок" value={String(tradesTotal)} />
          <StatCell
            label="Профит"
            value={`${pnlPos ? '+' : ''}${pnl.toFixed(2)}$`}
            color={pnlPos ? 'text-emerald-300' : 'text-rose-300'}
          />
          <StatCell
            label="Win"
            value={winRate !== null ? `${winRate}%` : '—'}
            color={winRate !== null && winRate >= 50 ? 'text-emerald-300' : 'text-rose-300'}
          />
        </div>

        {/* Кнопки */}
        <div className="flex gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="flex-1 rounded-lg border border-white/[.08] bg-white/[.04] py-2.5 text-sm font-semibold text-slate-300 hover:bg-white/[.08] transition-colors"
          >
            Отмена
          </button>
          <button
            type="button"
            onClick={onConfirm}
            className="flex-1 rounded-lg border border-rose-500/30 bg-rose-500/[.14] py-2.5 text-sm font-semibold text-rose-300 hover:bg-rose-500/[.22] transition-colors"
          >
            Обнулить и сохранить
          </button>
        </div>

        {/* Закрыть */}
        <button
          type="button"
          onClick={onCancel}
          className="absolute right-4 top-4 flex h-7 w-7 items-center justify-center rounded-full text-slate-500 hover:bg-white/[.06] hover:text-slate-300 transition-colors"
        >
          <svg width="13" height="13" viewBox="0 0 13 13" fill="none">
            <path d="M1 1l11 11M12 1L1 12" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round"/>
          </svg>
        </button>
      </div>
    </div>
  );
}

function StatCell({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div className="flex flex-col items-center py-3" style={{ background: 'rgba(255,255,255,0.025)' }}>
      <span className="mb-0.5 text-[9px] font-bold uppercase tracking-wider text-slate-500">{label}</span>
      <span className={`font-mono text-[14px] font-semibold ${color ?? 'text-slate-100'}`}>{value}</span>
    </div>
  );
}
