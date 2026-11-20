import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { Sidebar } from '../components/Sidebar'
import { isServerUnavailable } from '../api/accounts'

const base = {
  account: { exchangeBadge: 'B', name: '2026', exchange: 'bybit', status: 'подключено' },
  equity: 0,
  pnl24h: { percent: 0, usd: 0 },
  spark: [0, 0],
  novabotBalance: 0,
  user: { initials: 'AB', name: 'ab', email: 'ab@x.io' },
}

function renderSidebar(extra: Record<string, unknown> = {}) {
  return render(
    <MemoryRouter>
      <Sidebar {...base} {...extra} />
    </MemoryRouter>,
  )
}

test('shows "Сервер недоступен" instead of a zero balance when the server side is down', () => {
  renderSidebar({ serverUnavailable: true })
  expect(screen.getByText('Сервер недоступен')).toBeInTheDocument()
  // the equity figure carries the "USDT" unit; the NovaBot balance block below it is separate
  expect(screen.queryByText('USDT')).not.toBeInTheDocument()
})

test('shows the equity as usual when the server is reachable', () => {
  renderSidebar({ equity: 176.87 })
  expect(screen.getByText(/\$176\.87/)).toBeInTheDocument()
  expect(screen.queryByText('Сервер недоступен')).not.toBeInTheDocument()
})

test('isServerUnavailable only fires for the proxy-layer failure code', () => {
  expect(isServerUnavailable({ ok: false, code: 'server_unavailable' })).toBe(true)
  expect(isServerUnavailable({ ok: false })).toBe(false) // e.g. invalid API key: a real, other error
  expect(isServerUnavailable({ ok: true, code: 'server_unavailable' })).toBe(false)
  expect(isServerUnavailable(null)).toBe(false)
})
