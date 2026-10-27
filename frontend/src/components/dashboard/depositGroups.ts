import type { DepositDay } from '../../api/dashboard'

export type DepositGroup = 'day' | 'week' | 'month'

export interface DepositSlice extends DepositDay {
  /** main human-readable label, e.g. "Вс, 26 октября" / "20–26 октября" / "Октябрь 2026" */
  label: string
  /** small secondary line, e.g. "2026" or "неделя 43" */
  sublabel: string
  /** number of days merged into this slice */
  span: number
  drawdown: Drawdown | null
}

const MONTHS = ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня', 'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря']
const MONTHS_NOM = ['Январь', 'Февраль', 'Март', 'Апрель', 'Май', 'Июнь', 'Июль', 'Август', 'Сентябрь', 'Октябрь', 'Ноябрь', 'Декабрь']
const MONTHS_SHORT = ['янв', 'фев', 'мар', 'апр', 'мая', 'июн', 'июл', 'авг', 'сен', 'окт', 'ноя', 'дек']
const WEEKDAYS = ['Вс', 'Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб']

// "YYYY-MM-DD" → UTC Date (never shifted by the browser's timezone)
const parse = (iso: string) => {
  const [y, m, d] = iso.split('-').map(Number)
  return new Date(Date.UTC(y, m - 1, d))
}
const iso = (d: Date) => d.toISOString().slice(0, 10)

/** Monday of the ISO week containing d (UTC). */
function weekStart(d: Date): Date {
  const wd = (d.getUTCDay() + 6) % 7 // Mon=0
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate() - wd))
}

/** ISO-8601 week number. */
function isoWeek(d: Date): number {
  const t = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()))
  t.setUTCDate(t.getUTCDate() + 4 - (t.getUTCDay() || 7))
  const y0 = new Date(Date.UTC(t.getUTCFullYear(), 0, 1))
  return Math.ceil(((t.getTime() - y0.getTime()) / 86400000 + 1) / 7)
}

/**
 * Return of a result on the capital actually at work: opening equity plus what was deposited
 * meanwhile. null when there is no capital to relate it to (avoids dividing by ~0).
 */
export function returnPct(open: number, deposits: number, result: number): number | null {
  const capital = open + deposits
  return capital > 0 ? (result / capital) * 100 : null
}

export interface Drawdown {
  /** floating PnL at the last snapshot of the span (negative = drawdown) */
  unrealized: number
  unrealizedPct: number | null
  /** equity without floating PnL at the last snapshot */
  clean: number | null
  /** worst floating PnL of any snapshot in the span (≤ 0), with its % of equity */
  worstUsd: number | null
  worstPct: number | null
}

/**
 * Drawdown figures for a span of days (ascending): the close-of-span floating PnL comes from
 * the last day that has one; the worst is the deepest of the days' own worsts. null when no
 * day in the span carries drawdown data (history from before it was captured).
 */
export function drawdownOf(days: DepositDay[]): Drawdown | null {
  let last: DepositDay | null = null
  let worst: DepositDay | null = null
  for (const d of days) {
    if (d.unrealized_usd != null) last = d
    if (d.dd_worst_usd != null && (worst === null || d.dd_worst_usd < (worst.dd_worst_usd as number))) worst = d
  }
  if (!last) return null
  return {
    unrealized: last.unrealized_usd as number,
    unrealizedPct: last.unrealized_pct,
    clean: last.clean_close,
    worstUsd: worst ? worst.dd_worst_usd : null,
    worstPct: worst ? worst.dd_worst_pct : null,
  }
}

export function dayLabel(dayIso: string, currentYear: number): { label: string; sublabel: string } {
  const d = parse(dayIso)
  const y = d.getUTCFullYear()
  return {
    label: `${WEEKDAYS[d.getUTCDay()]}, ${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]}`,
    sublabel: y === currentYear ? '' : String(y),
  }
}

function weekLabel(start: Date): { label: string; sublabel: string } {
  const end = new Date(Date.UTC(start.getUTCFullYear(), start.getUTCMonth(), start.getUTCDate() + 6))
  const sameMonth = start.getUTCMonth() === end.getUTCMonth()
  const label = sameMonth
    ? `${start.getUTCDate()}–${end.getUTCDate()} ${MONTHS[end.getUTCMonth()]}`
    : `${start.getUTCDate()} ${MONTHS_SHORT[start.getUTCMonth()]} – ${end.getUTCDate()} ${MONTHS_SHORT[end.getUTCMonth()]}`
  return { label, sublabel: `неделя ${isoWeek(start)} · ${end.getUTCFullYear()}` }
}

/**
 * Merges per-day rows (ascending) into day / week (Mon–Sun, UTC) / month slices. Open is the
 * first day's open, close the last day's close; result and its components are summed, so a
 * slice's parts still add up to its result exactly. Returned ascending.
 */
export function groupDepositDays(days: DepositDay[], mode: DepositGroup, now: Date = new Date()): DepositSlice[] {
  const year = now.getUTCFullYear()
  const buckets = new Map<string, DepositDay[]>()
  for (const d of days) {
    const dt = parse(d.day)
    const key = mode === 'day' ? d.day : mode === 'week' ? iso(weekStart(dt)) : d.day.slice(0, 7)
    const list = buckets.get(key)
    if (list) list.push(d)
    else buckets.set(key, [d])
  }
  const out: DepositSlice[] = []
  for (const [key, list] of buckets) {
    const first = list[0]
    const last = list[list.length - 1]
    const sum = (k: keyof DepositDay) => list.reduce((s, d) => s + (d[k] as number), 0)
    const result = sum('result_usd')
    const drawdown = drawdownOf(list)
    let names: { label: string; sublabel: string }
    if (mode === 'day') names = dayLabel(first.day, year)
    else if (mode === 'week') names = weekLabel(parse(key))
    else {
      const [y, m] = key.split('-').map(Number)
      names = { label: `${MONTHS_NOM[m - 1]} ${y}`, sublabel: '' }
    }
    out.push({
      ...names,
      span: list.length,
      drawdown,
      day: first.day,
      equity_open: first.equity_open,
      equity_close: last.equity_close,
      change_usd: sum('change_usd'),
      result_usd: result,
      result_pct: returnPct(first.equity_open, sum('deposits_usd'), result) ?? 0,
      realized_usd: sum('realized_usd'),
      fees_usd: sum('fees_usd'),
      funding_usd: sum('funding_usd'),
      positions_usd: sum('positions_usd'),
      deposits_usd: sum('deposits_usd'),
      withdrawals_usd: sum('withdrawals_usd'),
      trades: sum('trades'),
      live: list.some(d => d.live),
      unrealized_usd: last.unrealized_usd,
      unrealized_pct: last.unrealized_pct,
      clean_close: last.clean_close,
      estimated: list.some(d => d.estimated),
      dd_worst_usd: drawdown ? drawdown.worstUsd : null,
      dd_worst_pct: drawdown ? drawdown.worstPct : null,
    })
  }
  return out
}
