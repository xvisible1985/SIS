// frontend/src/features/log-visualizer/LogVisualizerControls.tsx

import type { MergedEvent } from './types'

interface Props {
  isPlaying:    boolean
  speed:        number       // 1–48
  isMax:        boolean      // true = MAX mode (no animation, instant jump)
  currentEvent: MergedEvent | null
  hasData:      boolean
  canGoPrev:    boolean
  canGoNext:    boolean
  onPlay:       () => void
  onPause:      () => void
  onPrev:       () => void
  onNext:       () => void
  onFirst:      () => void
  onLast:       () => void
  onSpeedChange: (v: number) => void
  onMaxChange:   (v: boolean) => void
}

export function LogVisualizerControls({
  isPlaying, speed, isMax, currentEvent, hasData,
  canGoPrev, canGoNext,
  onPlay, onPause, onPrev, onNext, onFirst, onLast,
  onSpeedChange, onMaxChange,
}: Props) {
  function fmtTime(tsMs: number) {
    return new Date(tsMs).toLocaleString('ru-RU', {
      day: 'numeric', month: 'numeric',
      hour: '2-digit', minute: '2-digit', second: '2-digit',
    })
  }

  const btnBase = 'rounded px-3 py-1 text-xs font-medium transition-colors disabled:opacity-30 disabled:cursor-not-allowed'
  const btnSecondary = `${btnBase} bg-white/[.05] text-slate-300 hover:bg-white/[.09]`
  const btnPrimary   = `${btnBase} bg-[#5b8cff]/20 text-[#b8c8ff] hover:bg-[#5b8cff]/30`

  return (
    <div className="flex-shrink-0 border-t border-white/[.06] bg-[#0a0d14] px-4 py-2 flex flex-col gap-2">
      {/* Buttons row */}
      <div className="flex items-center gap-2">
        <button className={btnSecondary} disabled={!hasData || !canGoPrev} onClick={onFirst} title="В начало">⏮</button>
        <button className={btnSecondary} disabled={!hasData || !canGoPrev} onClick={onPrev} title="Предыдущее событие">◀ Пред.</button>

        {isPlaying ? (
          <button className={btnPrimary} onClick={onPause} title="Пауза">⏸ Пауза</button>
        ) : (
          <button className={btnPrimary} disabled={!hasData} onClick={onPlay} title="Играть">▶ Играть</button>
        )}

        <button className={btnSecondary} disabled={!hasData || !canGoNext} onClick={onNext} title="Следующее событие">След. ▶</button>
        <button className={btnSecondary} disabled={!hasData || !canGoNext} onClick={onLast} title="В конец">⏭</button>

        {/* Speed */}
        <div className="ml-auto flex items-center gap-2">
          <label className="flex items-center gap-1.5 cursor-pointer select-none text-[11px] text-slate-400">
            <input
              type="checkbox"
              checked={isMax}
              onChange={e => onMaxChange(e.target.checked)}
              className="accent-amber-400"
            />
            MAX
          </label>
          {!isMax && (
            <>
              <span className="text-[10px] text-slate-500 font-mono">{speed}×</span>
              <input
                type="range" min={1} max={48} step={1}
                value={speed}
                onChange={e => onSpeedChange(Number(e.target.value))}
                className="w-24 h-1 accent-[#5b8cff] cursor-pointer"
              />
            </>
          )}
        </div>
      </div>

      {/* Info line */}
      <div className="min-h-[16px]">
        {currentEvent ? (
          <div className="flex items-center gap-2">
            <span className="text-[10px] font-mono text-slate-500">{fmtTime(currentEvent.tsMs)}</span>
            <span className={`h-1.5 w-1.5 rounded-full shrink-0 ${
              currentEvent.kind === 'level'
                ? currentEvent.level?.side === 'Buy' ? 'bg-emerald-400' : 'bg-rose-400'
                : currentEvent.log?.level === 'error' ? 'bg-rose-400'
                  : currentEvent.log?.level === 'warn' ? 'bg-amber-400' : 'bg-slate-500'
            }`} />
            <span className="text-[11px] text-amber-200/90">{currentEvent.label}</span>
          </div>
        ) : (
          <span className="text-[10px] text-slate-600">
            {hasData ? 'Нажми ▶ Играть' : 'Загрузи данные'}
          </span>
        )}
      </div>
    </div>
  )
}
