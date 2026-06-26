import { useState } from 'react'
import { BybitNewsTab } from '../bybit-news/BybitNewsTab'
import { WhaleParserTab } from '../whale-parser/WhaleParserTab'

type ParserTab = 'listings' | 'whales'

export function ParsersSection() {
  const [tab, setTab] = useState<ParserTab>('listings')

  return (
    <div className="flex flex-col h-full">
      {/* Tab switcher */}
      <div className="flex gap-1 px-4 pt-4 pb-0">
        {([
          { key: 'listings', label: 'Листинги' },
          { key: 'whales',   label: 'Киты' },
        ] as { key: ParserTab; label: string }[]).map(t => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            className={`px-3 py-1.5 rounded-t-lg text-xs font-medium transition-colors ${
              tab === t.key
                ? 'bg-white/[.06] text-slate-200 border border-b-transparent border-white/[.08]'
                : 'text-slate-500 hover:text-slate-300'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div className="flex-1 overflow-y-auto border-t border-white/[.06]">
        {tab === 'listings' && <BybitNewsTab />}
        {tab === 'whales'   && <WhaleParserTab />}
      </div>
    </div>
  )
}
