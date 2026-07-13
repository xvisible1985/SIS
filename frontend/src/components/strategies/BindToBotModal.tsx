import { useState } from 'react'
import { bindStrategiesToBot } from '../../api/strategies'
import type { Strategy } from '../../types'
import type { Bot } from '../../features/bots/types'

export function BindToBotModal({
  stratA, stratB, bots, hasOpenPosition, originBotId, onClose, onBound,
}: {
  stratA: Strategy; stratB: Strategy; bots: Bot[]
  hasOpenPosition: boolean; originBotId?: string | null
  onClose: () => void; onBound: () => void
}) {
  const eligible = bots.filter(b =>
    b.accountId === stratA.account_id &&
    (b.strategyConfig?.bot_kind === 'hedge' || b.strategyConfig?.bot_kind === 'matrix')
  )
  const [sel, setSel] = useState<string>(originBotId && eligible.some(b => b.id === originBotId) ? originBotId : '')
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit() {
    setBusy(true); setErr(null)
    try {
      await bindStrategiesToBot(stratA.id, stratB.id, sel)
      onBound()
    } catch (e: any) {
      setErr(e?.response?.data?.error ?? 'Ошибка привязки')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={onClose}>
      <div className="bg-gray-900 border border-gray-700 rounded-xl w-[420px] shadow-2xl p-5" onClick={e => e.stopPropagation()}>
        <div className="text-[15px] font-bold text-gray-100 mb-3">Привязать к боту — {stratA.symbol}</div>
        {hasOpenPosition && (
          <div className="text-[12px] text-amber-300 bg-amber-400/10 border border-amber-500/20 rounded-lg px-3 py-2 mb-3">
            ⚠ По одной из лег есть открытая позиция — конфиг бота применится к ней (сетка/TP/SL перевыставятся).
          </div>
        )}
        <div className="max-h-[240px] overflow-y-auto flex flex-col gap-1 mb-3">
          {eligible.length === 0 && <div className="text-[12px] text-gray-500">Нет подходящих hedge/matrix ботов на этом аккаунте.</div>}
          {eligible.map(b => (
            <button key={b.id} onClick={() => setSel(b.id)}
              className={`flex items-center justify-between px-3 py-2 rounded-lg text-[13px] transition-colors ${sel === b.id ? 'bg-emerald-500/20 text-emerald-200' : 'bg-white/5 text-gray-300 hover:bg-white/[.08]'}`}>
              <span>{b.name} <span className="text-gray-500">· {b.strategyConfig?.bot_kind}</span></span>
              {b.id === originBotId && <span className="text-[10px] uppercase text-emerald-400">источник</span>}
            </button>
          ))}
        </div>
        {err && <div className="text-[12px] text-rose-400 mb-2">{err}</div>}
        <div className="flex justify-end gap-2">
          <button onClick={onClose} className="px-3 py-1.5 rounded-lg text-[13px] text-gray-400 hover:text-gray-200">Отмена</button>
          <button onClick={submit} disabled={!sel || busy}
            className="px-3 py-1.5 rounded-lg text-[13px] bg-emerald-500/20 text-emerald-200 hover:bg-emerald-500/30 disabled:opacity-50 disabled:hover:bg-emerald-500/20">
            {busy ? '…' : 'Привязать'}
          </button>
        </div>
      </div>
    </div>
  )
}
