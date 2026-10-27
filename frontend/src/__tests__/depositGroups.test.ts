import { groupDepositDays, drawdownOf } from '../components/dashboard/depositGroups'
import type { DepositDay } from '../api/dashboard'

const d = (day: string, open: number, close: number, result: number, over: Partial<DepositDay> = {}): DepositDay => ({
  day, equity_open: open, equity_close: close, change_usd: close - open, result_usd: result, result_pct: 0,
  realized_usd: result, fees_usd: 0, funding_usd: 0, positions_usd: 0, deposits_usd: 0, withdrawals_usd: 0,
  trades: 1, live: false,
  unrealized_usd: null, unrealized_pct: null, clean_close: null, dd_worst_usd: null, dd_worst_pct: null, ...over,
})

const NOW = new Date(Date.UTC(2026, 9, 27))

test('день: подробная подпись с днём недели, год показывается только для чужого года', () => {
  const [a] = groupDepositDays([d('2026-10-26', 100, 110, 10)], 'day', NOW)
  expect(a.label).toBe('Пн, 26 октября')
  expect(a.sublabel).toBe('')
  const [b] = groupDepositDays([d('2025-12-31', 100, 110, 10)], 'day', NOW)
  expect(b.label).toBe('Ср, 31 декабря')
  expect(b.sublabel).toBe('2025')
})

test('неделя (пн–вс): сумма результатов, открытие первого дня, закрытие последнего', () => {
  const days = [
    d('2026-10-19', 1000, 1010, 10), d('2026-10-21', 1010, 1040, 30, { deposits_usd: 5 }),
    d('2026-10-25', 1040, 1030, -10), d('2026-10-26', 1030, 1050, 20),
  ]
  const slices = groupDepositDays(days, 'week', NOW)
  expect(slices.map(s => s.label)).toEqual(['19–25 октября', '26 окт – 1 ноя'])
  expect(slices[0]).toMatchObject({ equity_open: 1000, equity_close: 1030, result_usd: 30, deposits_usd: 5, trades: 3, span: 3 })
  expect(slices[0].result_pct).toBeCloseTo(2.985, 3) // 30 / (1000 open + 5 deposited)
  expect(slices[1]).toMatchObject({ equity_open: 1030, equity_close: 1050, result_usd: 20 })
})

test('месяц: сводит дни месяца, компоненты остаются в сумме равны результату', () => {
  const days = [
    d('2026-09-30', 900, 950, 50, { fees_usd: -5, realized_usd: 55 }),
    d('2026-10-01', 950, 1000, 50, { fees_usd: -5, realized_usd: 40, positions_usd: 15 }),
    d('2026-10-02', 1000, 1010, 10, { realized_usd: 10 }),
  ]
  const slices = groupDepositDays(days, 'month', NOW)
  expect(slices.map(s => s.label)).toEqual(['Сентябрь 2026', 'Октябрь 2026'])
  const oct = slices[1]
  expect(oct.result_usd).toBe(60)
  expect(oct.realized_usd + oct.fees_usd + oct.funding_usd + oct.positions_usd).toBe(oct.result_usd)
})

test('срез помечен live, если внутри есть текущий день', () => {
  const s = groupDepositDays([d('2026-10-20', 1, 2, 1), d('2026-10-26', 2, 3, 1, { live: true })], 'week', NOW)
  expect(s.find(x => x.live)).toBeTruthy()
})

test('просадка среза: закрытие — с последнего дня, максимум — самый глубокий из дней', () => {
  const days = [
    d('2026-10-20', 1000, 1000, 0, { unrealized_usd: -10, unrealized_pct: -1, clean_close: 1010, dd_worst_usd: -120, dd_worst_pct: -11 }),
    d('2026-10-21', 1000, 1000, 0, { unrealized_usd: 5, unrealized_pct: 0.5, clean_close: 995, dd_worst_usd: -30, dd_worst_pct: -3 }),
  ]
  const [w] = groupDepositDays(days, 'week', NOW)
  expect(w.drawdown).toEqual({ unrealized: 5, unrealizedPct: 0.5, clean: 995, worstUsd: -120, worstPct: -11 })
  expect(drawdownOf([d('2026-10-19', 1, 1, 0)])).toBeNull() // no data → nothing shown
})

test('доходность считается от капитала в работе, а не от копеечного стартового остатка', () => {
  const [s] = groupDepositDays([d('2026-10-18', 0.04, 1158.03, -17.85, { deposits_usd: 1175.83, change_usd: 1157.99 })], 'day', NOW)
  expect(s.result_pct).toBeGreaterThan(-2)
  expect(s.result_pct).toBeLessThan(-1)
  expect(s.change_usd).toBeCloseTo(1157.99, 6)
})
