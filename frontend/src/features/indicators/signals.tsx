import { RSI, MACD, EMA, SMA, BollingerBands, Stochastic, ATR } from 'technicalindicators'
import type { SignalDef, Candle, SignalState } from './types';

// ── RSI Test manual override (set by RsiTestOverride in AdminPage) ─────────
let _rsiTestOverride: number | null = null
export function setRsiTestOverride(v: number | null) { _rsiTestOverride = v }

const op   = (s: React.ReactNode) => <span className="text-slate-400">{s}</span>;
const name = (s: React.ReactNode) => <span className="font-semibold text-[#b8c8ff]">{s}</span>;
const num  = (s: React.ReactNode) => <span className="text-emerald-300">{s}</span>;

const last = <T,>(arr: T[]): T | null => arr.length ? arr[arr.length - 1]! : null

function stDir(c: Candle[], period: number, mult: number): 'buy' | 'sell' | 'neutral' {
  if (c.length < period + 1) return 'neutral'
  const h = c.map(x => x.high), l = c.map(x => x.low), cl = c.map(x => x.close)
  const atrArr = ATR.calculate({ high: h, low: l, close: cl, period })
  if (!atrArr.length) return 'neutral'
  const off = c.length - atrArr.length
  let fub = 0, flb = 0, dir: 'buy' | 'sell' = 'sell'
  for (let i = 0; i < atrArr.length; i++) {
    const idx = i + off
    const mid = (h[idx]! + l[idx]!) / 2
    const bub = mid + mult * atrArr[i]!, blb = mid - mult * atrArr[i]!
    if (i === 0) { fub = bub; flb = blb; continue }
    const pc = cl[idx - 1]!
    fub = bub < fub || pc > fub ? bub : fub
    flb = blb > flb || pc < flb ? blb : flb
    dir = dir === 'sell' ? (cl[idx]! > fub ? 'buy' : 'sell') : (cl[idx]! < flb ? 'sell' : 'buy')
  }
  return dir
}

type RsiOsP   = { period: number; threshold: number; kind: string; tf: string }
type MacdXP   = { fast: number; slow: number; signal: number; dir: string; tf: string }
type GcP      = { fast: number; slow: number; confirm: string; tf: string }
type BbSqP    = { period: number; std: number; width: number; tf: string }
type StochXP  = { k: number; d: number; zone: number; tf: string }
type VolSpikeP = { period: number; mult: number; candle: string; tf: string }
type BreakoutP = { period: number; buffer: number; dir: string; tf: string }
type EmaXP    = { fast: number; slow: number; dir: string; tf: string }
type DivP     = { period: number; lookback: number; dir: string; tf: string }
type StFlipP  = { atr: number; mult: number; dir: string; ttl: number; tf: string }

export const SIGNALS: SignalDef<any>[] = [
  {
    id: 'rsi-os', abbr: 'RSI', name: 'RSI Oversold', cat: 'momentum', state: 'buy' as const,
    desc: 'Перепроданность — RSI пробивает нижнюю границу',
    about: 'Фиксирует вход RSI в зону перепроданности. Режим «cross» срабатывает только в момент пересечения порога — наиболее строгий. «enter» — при первом баре ниже порога. «stay» — пока RSI остаётся ниже (много сигналов). Наиболее эффективен на М15 и выше совместно с трендовым фильтром.',
    defaults: { period: 14, threshold: 30, kind: 'cross', tf: '1h' },
    params: [
      { kind: 'number',    key: 'period',    label: 'RSI период', hint: 'Период RSI для расчёта. Стандарт: 14.' },
      { kind: 'number',    key: 'threshold', label: 'Порог',      hint: 'Нижняя граница зоны перепроданности. Стандарт: 30.' },
      { kind: 'segmented', key: 'kind',      label: 'Режим',      hint: 'cross — только момент пересечения. enter — первый бар в зоне. stay — пока RSI в зоне.', options: ['cross', 'enter', 'stay'] },
    ],
    formula: p => <>{name(`RSI(${p.period})`)} {op('<')} {num(p.threshold)} {op('&& крест вверх')}</>,
    compute: (p: RsiOsP, c: Candle[]): SignalState => {
      if (c.length < p.period + 2) return 'neutral'
      const arr = RSI.calculate({ values: c.map(x => x.close), period: p.period })
      const v = last(arr), prev = arr[arr.length - 2]
      if (v === null || prev === undefined) return 'neutral'
      if (p.kind === 'cross')  return prev >= p.threshold && v < p.threshold ? 'buy' : 'neutral'
      if (p.kind === 'enter')  return prev > p.threshold && v <= p.threshold ? 'buy' : 'neutral'
      return v < p.threshold ? 'buy' : 'neutral'
    },
  },
  {
    id: 'macd-x', abbr: 'MX', name: 'MACD Crossover', cat: 'momentum', state: 'buy' as const,
    desc: 'Пересечение MACD и сигнальной линии',
    about: 'Сигнал пересечения MACD-линии и сигнальной: гистограмма меняет знак. Переход с отрицательной в положительную → Buy. С положительной в отрицательную → Sell. Лучше работает при ADX > 25 (трендовый рынок). В боковике генерирует много ложных сигналов.',
    defaults: { fast: 12, slow: 26, signal: 9, dir: 'вверх', tf: '1h' },
    params: [
      { kind: 'number',    key: 'fast',   label: 'Fast',        hint: 'Период быстрой EMA. Стандарт: 12.' },
      { kind: 'number',    key: 'slow',   label: 'Slow',        hint: 'Период медленной EMA. Стандарт: 26.' },
      { kind: 'number',    key: 'signal', label: 'Signal',      hint: 'Сглаживание сигнальной линии. Стандарт: 9.' },
      { kind: 'segmented', key: 'dir',    label: 'Направление', hint: 'вверх — только Buy. вниз — только Sell. оба — оба сигнала.', options: ['вверх', 'вниз', 'оба'] },
    ],
    formula: p => <>{name(`MACD(${p.fast},${p.slow})`)} {op('пересекает')} {name(`Signal(${p.signal})`)} {op(p.dir)}</>,
    compute: (p: MacdXP, c: Candle[]): SignalState => {
      if (c.length < p.slow + p.signal + 1) return 'neutral'
      const arr = MACD.calculate({ values: c.map(x => x.close), fastPeriod: p.fast, slowPeriod: p.slow, signalPeriod: p.signal, SimpleMAOscillator: false, SimpleMASignal: false })
      const v = last(arr), prev = arr[arr.length - 2]
      if (!v || !prev) return 'neutral'
      const h = v.histogram ?? 0, ph = prev.histogram ?? 0
      if (p.dir === 'вверх') return ph < 0 && h >= 0 ? 'buy' : 'neutral'
      if (p.dir === 'вниз')  return ph > 0 && h <= 0 ? 'sell' : 'neutral'
      if (ph < 0 && h >= 0) return 'buy'
      if (ph > 0 && h <= 0) return 'sell'
      return 'neutral'
    },
  },
  {
    id: 'gc', abbr: 'GC', name: 'Golden Cross', cat: 'trend', state: 'buy' as const,
    desc: 'EMA50 пересекает EMA200 снизу вверх',
    about: 'Golden Cross: быстрая MA пересекает медленную снизу вверх → долгосрочный бычий сигнал. Death Cross: обратное → медвежий. EMA реагирует быстрее SMA, но даёт больше ложных сигналов. Подтверждение «1bar/3bar» — сигнал выдаётся после закрытия N баров после пересечения.',
    defaults: { fast: 50, slow: 200, confirm: '1bar', tf: '1D' },
    params: [
      { kind: 'number',    key: 'fast',    label: 'Fast EMA',   hint: 'Период быстрой EMA. Обычно 50.', step: 5 },
      { kind: 'number',    key: 'slow',    label: 'Slow EMA',   hint: 'Период медленной EMA. Обычно 200.', step: 10 },
      { kind: 'segmented', key: 'confirm', label: 'Подтвержд.', hint: 'нет — сигнал в момент пересечения. 1bar/3bar — ждать закрытия N баров для подтверждения.', options: ['нет', '1bar', '3bar'] },
    ],
    formula: p => <>{name(`EMA(${p.fast})`)} {op('↑ пересекает')} {name(`EMA(${p.slow})`)}</>,
    compute: (p: GcP, c: Candle[]): SignalState => {
      if (c.length < p.slow + 3) return 'neutral'
      const cl = c.map(x => x.close)
      const fast = EMA.calculate({ values: cl, period: p.fast })
      const slow = EMA.calculate({ values: cl, period: p.slow })
      const len = Math.min(fast.length, slow.length)
      if (len < 2) return 'neutral'
      const f0 = fast[fast.length - 1]!, f1 = fast[fast.length - 2]!
      const s0 = slow[slow.length - 1]!, s1 = slow[slow.length - 2]!
      if (f1 <= s1 && f0 > s0) return 'buy'
      if (f1 >= s1 && f0 < s0) return 'sell'
      return f0 > s0 ? 'buy' : 'sell'
    },
  },
  {
    id: 'bb-sq', abbr: 'BBS', name: 'BB Squeeze', cat: 'volatility', state: 'neutral' as const,
    desc: 'Сжатие Bollinger Bands — низкая волатильность',
    about: 'Squeeze происходит когда ширина Bollinger Bands падает ниже порогового значения: (Upper−Lower)/Middle × 100 < threshold%. Узкие полосы — накопление энергии перед резким движением. Направление выброса не определяется — используйте с ADX или EMA-трендом для подтверждения.',
    defaults: { period: 20, std: 2.0, width: 3.0, tf: '1h' },
    params: [
      { kind: 'number', key: 'period', label: 'BB период', hint: 'Период SMA для расчёта Bollinger Bands. Стандарт: 20.' },
      { kind: 'number', key: 'std',    label: 'Откл.',     hint: 'Ширина полос в стандартных отклонениях. Стандарт: 2.0.', suffix: 'σ', step: 0.1, decimals: 1 },
      { kind: 'number', key: 'width',  label: 'Ширина',   hint: 'Максимальная ширина BB в % для срабатывания Squeeze. Чем меньше — тем сильнее сжатие.', suffix: '%', step: 0.5, decimals: 1 },
    ],
    formula: p => <>{name(`BB(${p.period},${p.std}σ)`)} {op('ширина <')} {num(`${p.width}%`)}</>,
    compute: (p: BbSqP, c: Candle[]): SignalState => {
      if (c.length < p.period) return 'neutral'
      const v = last(BollingerBands.calculate({ values: c.map(x => x.close), period: p.period, stdDev: p.std }))
      if (!v || !v.middle) return 'neutral'
      const widthPct = ((v.upper - v.lower) / v.middle) * 100
      return widthPct < p.width ? 'neutral' : 'neutral'
    },
  },
  {
    id: 'stoch-x', abbr: 'SCX', name: 'Stochastic Cross', cat: 'momentum', state: 'buy' as const,
    desc: '%K пересекает %D в зоне перекупленности/перепроданности',
    about: 'Пересечение %K и %D в экстремальной зоне — точка разворота. Пересечение снизу вверх при %K < zone → Buy. Сверху вниз при %K > (100−zone) → Sell. Более точен чем уровневые сигналы: требует подтверждение разворота внутри зоны. В сильных трендах зоны могут не посещаться.',
    defaults: { k: 14, d: 3, zone: 20, tf: '1h' },
    params: [
      { kind: 'number', key: 'k',    label: '%K',   hint: 'Период %K (основная линия). Стандарт: 14.' },
      { kind: 'number', key: 'd',    label: '%D',   hint: 'Период %D (сигнальная линия, SMA от %K). Стандарт: 3.' },
      { kind: 'number', key: 'zone', label: 'Зона', hint: 'Нижняя граница зоны для Buy. Верхняя = 100 − zone. Стандарт: 20.', step: 5 },
    ],
    formula: p => <>{name('%K')} {op('×')} {name('%D')} {op('в зоне')} {num(`<${p.zone}`)}</>,
    compute: (p: StochXP, c: Candle[]): SignalState => {
      if (c.length < p.k + p.d + 1) return 'neutral'
      const arr = Stochastic.calculate({ high: c.map(x => x.high), low: c.map(x => x.low), close: c.map(x => x.close), period: p.k, signalPeriod: p.d })
      const v = last(arr), prev = arr[arr.length - 2]
      if (!v || !prev) return 'neutral'
      if (v.k < p.zone && prev.k <= prev.d && v.k > v.d) return 'buy'
      if (v.k > (100 - p.zone) && prev.k >= prev.d && v.k < v.d) return 'sell'
      return 'neutral'
    },
  },
  {
    id: 'vol-spike', abbr: 'VS', name: 'Volume Spike', cat: 'volume', state: 'neutral' as const,
    desc: 'Объём кратно превышает свою скользящую среднюю',
    about: 'Объём > MA(period) × mult — аномальный всплеск активности. Направление: бычья свеча → Buy, медвежья → Sell. Всплески объёма часто сопровождают пробои уровней или ключевые развороты. Эффективен в комбинации с Range Breakout для подтверждения пробоя объёмом.',
    defaults: { period: 20, mult: 2.5, candle: 'любая', tf: '15m' },
    params: [
      { kind: 'number',    key: 'period', label: 'SMA период', hint: 'Период MA объёма для базового уровня сравнения. Стандарт: 20.' },
      { kind: 'number',    key: 'mult',   label: 'Множитель', hint: 'Минимальное превышение MA для срабатывания. Например, 2.5× означает объём в 2.5 раза больше MA.', suffix: '×', step: 0.1, decimals: 1 },
      { kind: 'segmented', key: 'candle', label: 'Свеча',     hint: 'любая — сигнал при любой свече. зелён. — только бычьи (Buy). красн. — только медвежьи (Sell).', options: ['любая', 'зелён.', 'красн.'] },
    ],
    formula: p => <>{name('Volume')} {op('>')} {num(`${p.mult}×`)} {name(`SMA(${p.period})`)}</>,
    compute: (p: VolSpikeP, c: Candle[]): SignalState => {
      if (c.length < p.period + 1) return 'neutral'
      const vols = c.map(x => x.volume)
      const ma = last(SMA.calculate({ values: vols, period: p.period }))
      if (!ma) return 'neutral'
      const cur = vols[vols.length - 1]!
      if (cur < ma * p.mult) return 'neutral'
      const last_ = c[c.length - 1]!
      const isGreen = last_.close >= last_.open
      if (p.candle === 'зелён.' && !isGreen) return 'neutral'
      if (p.candle === 'красн.' && isGreen) return 'neutral'
      return isGreen ? 'buy' : 'sell'
    },
  },
  {
    id: 'breakout', abbr: 'BR', name: 'Range Breakout', cat: 'volatility', state: 'buy' as const,
    desc: 'Пробой максимума / минимума за N баров',
    about: 'Пробой Close за максимум или минимум N последних баров. Буфер (%) исключает ложные пробои: реальный пробой требует преодолеть High + High×buffer%. Режим up — восходящий, down — нисходящий, оба — любой. Рекомендуется использовать совместно с объёмным подтверждением.',
    defaults: { period: 20, buffer: 0.10, dir: 'up', tf: '1h' },
    params: [
      { kind: 'number',    key: 'period', label: 'Период',   hint: 'Число баров для определения диапазона High-Low. Больше → более значимый пробой.', step: 5 },
      { kind: 'number',    key: 'buffer', label: 'Буфер',    hint: 'Отступ в % от уровня для исключения ложных пробоев. 0.1% = цена должна пройти чуть выше High.', suffix: '%', step: 0.05, decimals: 2 },
      { kind: 'segmented', key: 'dir',    label: 'Направл.', hint: 'up — только пробои вверх (Buy). down — вниз (Sell). оба — оба направления.', options: ['up', 'down', 'оба'] },
    ],
    formula: p => (
      <>{name('close')} {op(p.dir === 'up' ? '>' : '<')} {name(`${p.dir === 'up' ? 'high' : 'low'}(${p.period})`)} {op(`+ ${p.buffer}%`)}</>
    ),
    compute: (p: BreakoutP, c: Candle[]): SignalState => {
      if (c.length < p.period + 1) return 'neutral'
      const window = c.slice(-(p.period + 1), -1)
      const price = c[c.length - 1]!.close
      const buf = p.buffer / 100
      if (p.dir !== 'down') {
        const maxH = Math.max(...window.map(x => x.high))
        if (price > maxH * (1 + buf)) return 'buy'
      }
      if (p.dir !== 'up') {
        const minL = Math.min(...window.map(x => x.low))
        if (price < minL * (1 - buf)) return 'sell'
      }
      return 'neutral'
    },
  },
  {
    id: 'ema-x', abbr: 'EMX', name: 'EMA Crossover', cat: 'trend', state: 'sell' as const,
    desc: 'Быстрая EMA пересекает медленную',
    about: 'Динамический Golden/Death Cross на EMA-базе. Реагирует быстрее чем SMA-кросс, но более шумный в боковике. Подходит для средне- и долгосрочной торговли на таймфреймах 1h–1D. При ADX < 20 сигналы лучше игнорировать.',
    defaults: { fast: 9, slow: 21, dir: 'вверх', tf: '1h' },
    params: [
      { kind: 'number',    key: 'fast', label: 'Fast',     hint: 'Период быстрой EMA. Обычно 9, 12, 20.' },
      { kind: 'number',    key: 'slow', label: 'Slow',     hint: 'Период медленной EMA. Обычно 21, 26, 50.' },
      { kind: 'segmented', key: 'dir',  label: 'Направл.', hint: 'вверх — только Buy-кроссы. вниз — Sell. оба — оба направления.', options: ['вверх', 'вниз', 'оба'] },
    ],
    formula: p => <>{name(`EMA(${p.fast})`)} {op('×')} {name(`EMA(${p.slow})`)} {op(p.dir)}</>,
    compute: (p: EmaXP, c: Candle[]): SignalState => {
      if (c.length < p.slow + 2) return 'neutral'
      const cl = c.map(x => x.close)
      const fast = EMA.calculate({ values: cl, period: p.fast })
      const slow = EMA.calculate({ values: cl, period: p.slow })
      const f0 = fast[fast.length - 1]!, f1 = fast[fast.length - 2]!
      const s0 = slow[slow.length - 1]!, s1 = slow[slow.length - 2]!
      if (p.dir === 'вверх') return f1 <= s1 && f0 > s0 ? 'buy' : 'neutral'
      if (p.dir === 'вниз')  return f1 >= s1 && f0 < s0 ? 'sell' : 'neutral'
      if (f1 <= s1 && f0 > s0) return 'buy'
      if (f1 >= s1 && f0 < s0) return 'sell'
      return 'neutral'
    },
  },
  {
    id: 'div', abbr: 'DIV', name: 'RSI Divergence', cat: 'momentum', state: 'sell' as const,
    desc: 'Дивергенция между ценой и RSI',
    about: 'Бычья дивергенция: цена делает новый Low, RSI — выше предыдущего Low → потенциальный разворот вверх. Медвежья: новый High цены, RSI ниже предыдущего High → разворот вниз. Один из сильнейших разворотных паттернов. Lookback определяет окно поиска предыдущего экстремума.',
    defaults: { period: 14, lookback: 50, dir: 'bull', tf: '1h' },
    params: [
      { kind: 'number',    key: 'period',   label: 'RSI период', hint: 'Период RSI для поиска дивергенции. Стандарт: 14.' },
      { kind: 'number',    key: 'lookback', label: 'Lookback',   hint: 'Число баров для поиска предыдущего экстремума. Больше → ищет более значимые дивергенции.', step: 5 },
      { kind: 'segmented', key: 'dir',      label: 'Тип',        hint: 'bull — бычья дивергенция (Buy). bear — медвежья (Sell). оба — оба типа.', options: ['bull', 'bear', 'оба'] },
    ],
    formula: p => (
      <>{name(`price.${p.dir === 'bull' ? 'low' : 'high'}`)} {op(p.dir === 'bull' ? '↓' : '↑')}, {name(`RSI(${p.period})`)} {op(p.dir === 'bull' ? '↑' : '↓')}</>
    ),
    compute: (p: DivP, c: Candle[]): SignalState => {
      const window = c.slice(-Math.min(p.lookback, c.length))
      if (window.length < p.period + 5) return 'neutral'
      const rsi = RSI.calculate({ values: window.map(x => x.close), period: p.period })
      const closes = window.map(x => x.close).slice(window.length - rsi.length)
      if (rsi.length < 4) return 'neutral'
      const n = rsi.length - 1
      const findPrev = (arr: number[], from: number, type: 'min' | 'max') => {
        for (let i = from - 2; i >= 1; i--) {
          if (type === 'min' && arr[i]! < arr[i - 1]! && arr[i]! < arr[i + 1]!) return i
          if (type === 'max' && arr[i]! > arr[i - 1]! && arr[i]! > arr[i + 1]!) return i
        }
        return -1
      }
      if (p.dir !== 'bear') {
        const pi2 = findPrev(closes, n, 'min')
        const ri2 = findPrev(rsi, n, 'min')
        if (pi2 > 0 && ri2 > 0) {
          if (closes[n]! < closes[pi2]! && rsi[n]! > rsi[ri2]!) return 'buy'
        }
      }
      if (p.dir !== 'bull') {
        const pi2 = findPrev(closes, n, 'max')
        const ri2 = findPrev(rsi, n, 'max')
        if (pi2 > 0 && ri2 > 0) {
          if (closes[n]! > closes[pi2]! && rsi[n]! < rsi[ri2]!) return 'sell'
        }
      }
      return 'neutral'
    },
  },
  {
    id: 'bybit-news', abbr: 'BN', name: 'Bybit News Listing', cat: 'fundamental', state: 'buy' as const,
    desc: 'Автоматический запуск по листингам Bybit',
    about: 'Сканирует анонсы Bybit о новых листингах. При появлении новости создаёт стратегию для указанного символа с плечом из анонса. Только long.\n\nВремя жизни — сколько минут после анонса бот может открыть стратегию. После истечения окна повторный вход невозможен даже если стратегия уже закрылась по TP/SL.',
    defaults: { lifetime_minutes: 60 },
    params: [
      { kind: 'number', key: 'lifetime_minutes', label: 'Время жизни, мин', hint: 'Окно от анонса, в течение которого можно открыть стратегию. После закрытия TP/SL повторный вход заблокирован до конца окна.', min: 1, max: 1440 },
    ],
    formula: () => <>{name('Bybit News Listing')}</>,
    compute: () => 'neutral' as const,
  },
  {
    id: 'st-flip', abbr: 'STF', name: 'SuperTrend', cat: 'trend', state: 'buy' as const,
    desc: 'Направление SuperTrend — Buy выше линии, Sell ниже',
    about: 'SuperTrend на базе ATR. Цена выше верхней полосы → Buy, ниже нижней → Sell. Генерирует меньше сигналов чем EMA-кросс, но более надёжен в трендовых условиях. Множитель 3+ рекомендован для долгосрочных позиций.',
    defaults: { atr: 10, mult: 3.0, dir: 'лонг', ttl: 0, tf: '1h' },
    params: [
      { kind: 'number',    key: 'atr',  label: 'ATR период',  hint: 'Период для расчёта ATR. Стандарт: 10.' },
      { kind: 'number',    key: 'mult', label: 'Множитель',   hint: 'Коэффициент ATR. Меньше → чаще флипы, больше → надёжнее. Стандарт: 3.0.', step: 0.1, decimals: 1 },
      { kind: 'segmented', key: 'dir',  label: 'Направл.',    hint: 'лонг — только переворот в Buy. short — только в Sell. оба — оба направления.', options: ['лонг', 'short', 'оба'] },
      { kind: 'number',    key: 'ttl',  label: 'TTL (мин)',   hint: 'Время жизни сигнала в минутах. После истечения сигнал переходит в Neutral и не запускает новые стратегии. 0 — без ограничений.', step: 1, decimals: 0 },
    ],
    formula: p => <>{name(`SuperTrend(${p.atr},${p.mult})`)} {op(`↻ ${p.dir}`)}</>,
    compute: (p: StFlipP, c: Candle[]): SignalState => {
      const dir = stDir(c, p.atr, p.mult)
      if (p.dir === 'bull' || p.dir === 'лонг') return dir === 'buy' ? 'buy' : 'neutral'
      if (p.dir === 'bear' || p.dir === 'short') return dir === 'sell' ? 'sell' : 'neutral'
      return dir
    },
  },
  {
    id: 'rsi-test', abbr: 'RSI_T', name: 'RSI Test', cat: 'momentum', state: 'buy' as const,
    desc: 'RSI с ручным вводом значения — для тестирования стратегий',
    about: 'Полный аналог RSI Oversold. Когда в админке активен ручной override — стратегия реагирует на вручную установленное значение RSI, игнорируя реальные свечи. Удобно для проверки логики входа/выхода без ожидания реального сигнала.',
    defaults: { period: 14, threshold: 30, kind: 'stay', tf: '1h' },
    params: [
      { kind: 'number',    key: 'period',    label: 'RSI период', hint: 'Период RSI. Стандарт: 14.' },
      { kind: 'number',    key: 'threshold', label: 'Порог',      hint: 'Нижняя граница зоны перепроданности. Buy когда RSI < порога.' },
      { kind: 'segmented', key: 'kind',      label: 'Режим',      hint: 'cross — только момент пересечения. enter — первый бар в зоне. stay — пока RSI в зоне.', options: ['cross', 'enter', 'stay'] },
    ],
    formula: p => <>{name(`RSI(${p.period})`)} {op('<')} {num((p as any).threshold)} {op('→ Buy')}</>,
    compute: (p: { period: number; threshold: number; kind: string; tf: string }, c: Candle[]): SignalState => {
      if (_rsiTestOverride !== null) {
        return _rsiTestOverride < p.threshold ? 'buy' : 'neutral'
      }
      if (c.length < p.period + 2) return 'neutral'
      const arr = RSI.calculate({ values: c.map(x => x.close), period: p.period })
      const v = last(arr), prev = arr[arr.length - 2]
      if (v === null || prev === undefined) return 'neutral'
      if (p.kind === 'cross') return prev >= p.threshold && v < p.threshold ? 'buy' : 'neutral'
      if (p.kind === 'enter') return prev > p.threshold && v <= p.threshold ? 'buy' : 'neutral'
      return v < p.threshold ? 'buy' : 'neutral'
    },
  },
]
