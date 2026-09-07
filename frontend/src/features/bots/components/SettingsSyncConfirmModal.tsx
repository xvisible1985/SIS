import { useEffect } from 'react';
import { GitBranch } from 'lucide-react';

type Props = {
  title:        string;
  description:  string;
  cancelLabel:  string;
  confirmLabel: string;
  onConfirm:    () => void;
  onCancel:     () => void;
};

/** Общая модалка подтверждения для обоих направлений синхронизации настроек
 *  «бот ↔ открытая стратегия» — см. docs/superpowers/specs/2026-09-07-bot-strategy-settings-sync-design.md */
export function SettingsSyncConfirmModal({ title, description, cancelLabel, confirmLabel, onConfirm, onCancel }: Props) {
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel();
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [onCancel]);

  return (
    <div
      className="fixed inset-0 z-[10000] flex items-center justify-center"
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
        <div className="mb-4 flex items-start gap-3">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border border-[#5b8cff]/25 bg-[#5b8cff]/[.12]">
            <GitBranch size={16} className="text-[#5b8cff]" strokeWidth={2} />
          </div>
          <div>
            <div className="font-display text-[16px] font-bold tracking-tight text-slate-50">
              {title}
            </div>
            <div className="mt-0.5 text-[12px] leading-[1.5] text-slate-400">
              {description}
            </div>
          </div>
        </div>

        <div className="flex gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="flex-1 rounded-lg border border-white/[.08] bg-white/[.04] py-2.5 text-sm font-semibold text-slate-300 hover:bg-white/[.08] transition-colors"
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            onClick={onConfirm}
            className="flex-1 rounded-lg border border-[#5b8cff]/40 bg-[#5b8cff]/[.18] py-2.5 text-sm font-semibold text-[#5b8cff] hover:bg-[#5b8cff]/[.28] transition-colors"
          >
            {confirmLabel}
          </button>
        </div>

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
