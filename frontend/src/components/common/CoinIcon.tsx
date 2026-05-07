import { useState } from 'react'

function hashColor(s: string): string {
  let h = 0
  for (let i = 0; i < s.length; i++) h = s.charCodeAt(i) + ((h << 5) - h)
  return `hsl(${Math.abs(h) % 360}, 55%, 38%)`
}

interface Props {
  symbol: string
  className?: string
}

export function CoinIcon({ symbol, className = 'w-7 h-7' }: Props) {
  const [failed, setFailed] = useState(false)
  const base = symbol.replace(/(?:USDT|USDC|USD)$/, '')
  const src = `https://cdn.jsdelivr.net/gh/spothq/cryptocurrency-icons/32/color/${base.toLowerCase()}.png`

  if (failed) {
    return (
      <div
        className={`${className} rounded-full flex items-center justify-center text-white font-bold flex-shrink-0`}
        style={{ background: hashColor(base), fontSize: base.length > 3 ? '7px' : '8px' }}
      >
        {base.slice(0, 4)}
      </div>
    )
  }

  return (
    <img
      src={src}
      className={`${className} rounded-full flex-shrink-0`}
      onError={() => setFailed(true)}
    />
  )
}
