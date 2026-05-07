import type { SignalConfig } from '../../types'

const SIGNAL_CATEGORY: Record<string, { color: string; icon: string }> = {
  RSI:           { color: 'text-purple-400', icon: '〜' },
  MACD:          { color: 'text-purple-400', icon: '⬆' },
  EMA:           { color: 'text-blue-400',   icon: '—' },
  SMA:           { color: 'text-blue-400',   icon: '—' },
  Bollinger:     { color: 'text-yellow-400', icon: '↔' },
  Stochastic:    { color: 'text-purple-400', icon: '〜' },
  ADX:           { color: 'text-blue-400',   icon: '▲' },
  CCI:           { color: 'text-purple-400', icon: '〜' },
  ATR:           { color: 'text-yellow-400', icon: '↕' },
  OBV:           { color: 'text-cyan-400',   icon: '↑' },
  'Williams %R': { color: 'text-purple-400', icon: '%' },
  MFI:           { color: 'text-cyan-400',   icon: '$' },
  SAR:           { color: 'text-blue-400',   icon: '●' },
  Ichimoku:      { color: 'text-blue-400',   icon: '≡' },
  Supertrend:    { color: 'text-blue-400',   icon: '⬆' },
  'Volume Spike':{ color: 'text-orange-400', icon: '⚡' },
  'ATR Breakout':{ color: 'text-indigo-400', icon: '↕' },
  Divergence:    { color: 'text-green-400',  icon: '⇌' },
}

export function SignalMiniCard({ signal }: { signal: SignalConfig }) {
  const cat = SIGNAL_CATEGORY[signal.name] ?? { color: 'text-gray-400', icon: '◆' }
  const paramStr = Object.entries(signal.params ?? {})
    .map(([, v]) => v)
    .join(', ')

  return (
    <div className="relative bg-gray-800/60 border border-gray-700 rounded-lg px-3 pt-6 pb-2 min-w-[110px] flex-1">
      <div className="absolute top-1.5 left-1.5">
        <span className="bg-gray-700 text-gray-400 text-[9px] px-1.5 py-0.5 rounded">Норма</span>
      </div>
      <div className="flex items-center gap-1.5 mb-1">
        <span className={`text-sm ${cat.color}`}>{cat.icon}</span>
        <span className="text-gray-300 text-[11px] font-semibold truncate">{signal.name}</span>
        {paramStr && <span className="text-gray-600 text-[9px]">({paramStr})</span>}
      </div>
      <div className="text-gray-100 text-sm font-bold font-mono">—</div>
    </div>
  )
}
