import {
  RSI, MACD, EMA, SMA, BollingerBands, Stochastic,
  ATR, ADX, IchimokuCloud, OBV, CCI, WilliamsR, MFI, PSAR, ROC
} from 'technicalindicators'
import type { IndicatorDef, BaseParams, Candle, PriceSource, IndicatorSignal, SignalState } from './types';
import { SOURCES, TFS } from './categories';
import { SparkLine, SparkBars, demo } from './components/Sparklines';

type RsiP   = BaseParams & { period: number; upper: number; lower: number; source: PriceSource };
type MacdP  = BaseParams & { fast: number; slow: number; signal: number; source: PriceSource };
type MaP    = BaseParams & { period: number; offset: number; source: PriceSource };
type BbP    = BaseParams & { period: number; std: number; source: PriceSource };
type StochP = BaseParams & { k: number; d: number; smooth: number };
type AtrP   = BaseParams & { period: number };
type AdxP   = BaseParams & { period: number; threshold: number; mode?: string };
type IchiP  = BaseParams & { tenkan: number; kijun: number; senkou: number; shift: number };
type VwapP  = BaseParams & { anchor: 'session' | 'day' | 'week' };
type CciP   = BaseParams & { period: number; upper: number; lower: number; source: PriceSource };
type WprP   = BaseParams & { period: number; upper: number; lower: number };
type PsarP  = BaseParams & { step: number; max: number };
type MfiP   = BaseParams & { period: number; upper: number; lower: number };
type VolP   = BaseParams & { maPeriod: number };
type RocP   = BaseParams & { period: number; source: PriceSource };
type StP    = BaseParams & { atr: number; mult: number };
type KcP    = BaseParams & { period: number; atr: number; mult: number };
type AoP    = BaseParams & { fast: number; slow: number };

const last = <T,>(arr: T[]): T | null => arr.length ? arr[arr.length - 1]! : null
const sig = (s: SignalState, v?: string): IndicatorSignal => ({ state: s, value: v })

function src(c: Candle[], p: PriceSource): number[] {
  return c.map(x =>
    p === 'open' ? x.open : p === 'high' ? x.high : p === 'low' ? x.low :
    p === 'hl2' ? (x.high + x.low) / 2 :
    p === 'ohlc4' ? (x.open + x.high + x.low + x.close) / 4 : x.close
  )
}

function stDir(candles: Candle[], period: number, mult: number): { state: SignalState; val: number } {
  const h = candles.map(c => c.high), l = candles.map(c => c.low), cl = candles.map(c => c.close)
  const atrArr = ATR.calculate({ high: h, low: l, close: cl, period })
  if (!atrArr.length) return { state: 'neutral', val: 0 }
  const offset = candles.length - atrArr.length
  let fub = 0, flb = 0, dir: 'buy' | 'sell' = 'sell'
  for (let i = 0; i < atrArr.length; i++) {
    const idx = i + offset
    const mid = (h[idx]! + l[idx]!) / 2
    const bub = mid + mult * atrArr[i]!, blb = mid - mult * atrArr[i]!
    if (i === 0) { fub = bub; flb = blb; continue }
    const pc = cl[idx - 1]!
    fub = bub < fub || pc > fub ? bub : fub
    flb = blb > flb || pc < flb ? blb : flb
    dir = dir === 'sell' ? (cl[idx]! > fub ? 'buy' : 'sell') : (cl[idx]! < flb ? 'sell' : 'buy')
  }
  return { state: dir, val: +(dir === 'buy' ? flb : fub).toFixed(2) }
}

export const INDICATORS: IndicatorDef<any>[] = [
  {
    id: 'rsi', abbr: 'RSI', name: 'RSI', cat: 'momentum',
    desc: 'Индекс относительной силы — перекупленность и перепроданность',
    about: 'RSI (0–100) вычисляется как RS = среднее роста / среднее падения за период. Значение выше верхнего порога — перекупленность (вероятный откат). Ниже нижнего — перепроданность (вероятный отскок). Лучше работает в боковых рынках; в сильных трендах RSI может долго оставаться в зоне перекупленности. Классический период: 14.',
    defaults: { period: 14, upper: 70, lower: 30, source: 'close', tf: '1h' } as RsiP,
    params: [
      { kind: 'number', key: 'period', label: 'Период',    hint: 'Число баров для расчёта. Меньше периода → чувствительнее, больше ложных сигналов. Классика: 14.',          min: 2 },
      { kind: 'number', key: 'upper',  label: 'Перекупл.', hint: 'Порог перекупленности. При RSI ≥ upper выдаётся сигнал Sell. Стандарт: 70.' },
      { kind: 'number', key: 'lower',  label: 'Перепрод.', hint: 'Порог перепроданности. При RSI ≤ lower выдаётся сигнал Buy. Стандарт: 30.' },
    ],
    compute: (p: RsiP, c: Candle[]) => {
      if (c.length < p.period + 1) return sig('neutral')
      const v = last(RSI.calculate({ values: src(c, p.source), period: p.period }))
      if (v === null) return sig('neutral')
      if (v <= p.lower) return sig('buy', v.toFixed(1))
      if (v >= p.upper) return sig('sell', v.toFixed(1))
      return sig('neutral', v.toFixed(1))
    },
    Preview: () => <SparkLine data={demo.sin(32, 2.5, 25, 50)} stroke="#b8c8ff" fill="#b8c8ff" hLines={[{ v: 70, c: '#fca5a5' }, { v: 30, c: '#5be0a0' }]} />,
  },
  {
    id: 'macd', abbr: 'MACD', name: 'MACD', cat: 'momentum',
    desc: 'Схождение / расхождение скользящих средних',
    about: 'MACD = EMA(fast) − EMA(slow). Сигнальная линия = EMA(signal) от MACD. Гистограмма = MACD − Signal. Положительная гистограмма: быстрая EMA ускоряется — бычий импульс. Пересечение нуля — смена направления импульса. Популярная комбинация: 12/26/9.',
    defaults: { fast: 12, slow: 26, signal: 9, source: 'close', tf: '1h' } as MacdP,
    params: [
      { kind: 'number', key: 'fast',   label: 'Fast',   hint: 'Период быстрой EMA. Классика: 12.' },
      { kind: 'number', key: 'slow',   label: 'Slow',   hint: 'Период медленной EMA. Классика: 26.' },
      { kind: 'number', key: 'signal', label: 'Signal', hint: 'Сглаживание сигнальной линии. Классика: 9.' },
    ],
    compute: (p: MacdP, c: Candle[]) => {
      if (c.length < p.slow + p.signal) return sig('neutral')
      const arr = MACD.calculate({ values: src(c, p.source), fastPeriod: p.fast, slowPeriod: p.slow, signalPeriod: p.signal, SimpleMAOscillator: false, SimpleMASignal: false })
      const v = last(arr)
      if (!v) return sig('neutral')
      const h = v.histogram ?? 0
      return sig(h > 0 ? 'buy' : h < 0 ? 'sell' : 'neutral', h > 0 ? `+${h.toFixed(4)}` : h.toFixed(4))
    },
    Preview: () => <SparkLine data={demo.sin(32, 1.8, 18, 50)} stroke="#5be0a0" mid={{ data: demo.sin(32, 1.8, 12, 50), c: '#fca5a5' }} />,
  },
  {
    id: 'ema', abbr: 'EMA', name: 'EMA', cat: 'trend',
    desc: 'Экспоненциальная скользящая средняя',
    about: 'EMA присваивает экспоненциально убывающий вес каждому предыдущему бару: k = 2/(period+1). Реагирует быстрее SMA на последние ценовые изменения. Используется как динамическая поддержка/сопротивление и основа для MACD. Цена выше EMA — восходящий тренд.',
    defaults: { period: 50, offset: 0, source: 'close', tf: '1h' } as MaP,
    params: [
      { kind: 'number', key: 'period', label: 'Период',   hint: 'Число баров для усреднения. Чем больше — тем плавнее линия, больше запаздывание.' },
      { kind: 'number', key: 'offset', label: 'Смещение', hint: 'Сдвиг линии вправо (+) или влево (−) на N баров для визуального выравнивания.' },
    ],
    compute: (p: MaP, c: Candle[]) => {
      if (c.length < p.period) return sig('neutral')
      const v = last(EMA.calculate({ values: src(c, p.source), period: p.period }))
      if (v === null) return sig('neutral')
      const price = c[c.length - 1]!.close
      return sig(price > v ? 'buy' : 'sell', v.toFixed(2))
    },
    Preview: () => <SparkLine data={demo.up()} stroke="#5be0a0" fill="#5be0a0" />,
  },
  {
    id: 'sma', abbr: 'SMA', name: 'SMA', cat: 'trend',
    desc: 'Простая скользящая средняя',
    about: 'SMA — среднее арифметическое цен за период. Медленнее и плавнее EMA, хорошо фильтрует краткосрочный шум. Пересечение двух SMA (Golden/Death Cross) — классический трендовый сигнал. Служит базой для Bollinger Bands и других индикаторов.',
    defaults: { period: 20, offset: 0, source: 'close', tf: '1h' } as MaP,
    params: [
      { kind: 'number', key: 'period', label: 'Период',   hint: 'Число баров для усреднения. Популярные значения: 20, 50, 100, 200.' },
      { kind: 'number', key: 'offset', label: 'Смещение', hint: 'Сдвиг линии вправо (+) или влево (−) на N баров.' },
    ],
    compute: (p: MaP, c: Candle[]) => {
      if (c.length < p.period) return sig('neutral')
      const v = last(SMA.calculate({ values: src(c, p.source), period: p.period }))
      if (v === null) return sig('neutral')
      const price = c[c.length - 1]!.close
      return sig(price > v ? 'buy' : 'sell', v.toFixed(2))
    },
    Preview: () => <SparkLine data={demo.up()} stroke="#7b8aa6" fill="#7b8aa6" />,
  },
  {
    id: 'bb', abbr: 'BB', name: 'Bollinger Bands', cat: 'volatility',
    desc: 'Канал стандартного отклонения вокруг средней',
    about: 'Полосы = SMA ± std×σ. %B = (Close − Lower) / (Upper − Lower). %B > 100% — цена выше верхней полосы; %B < 0% — ниже нижней. Сужение полос (Squeeze) предвещает резкое движение. В сильном тренде цена может «идти по полосе» — выход за полосу не всегда разворот.',
    defaults: { period: 20, std: 2.0, source: 'close', tf: '1h' } as BbP,
    params: [
      { kind: 'number', key: 'period', label: 'Период',     hint: 'Период SMA для средней линии. Стандарт: 20.' },
      { kind: 'number', key: 'std',    label: 'Отклонение', hint: 'Ширина полос в стандартных отклонениях (σ). 2σ охватывает ~95% данных.', step: 0.1, decimals: 1, suffix: 'σ' },
    ],
    compute: (p: BbP, c: Candle[]) => {
      if (c.length < p.period) return sig('neutral')
      const v = last(BollingerBands.calculate({ values: src(c, p.source), period: p.period, stdDev: p.std }))
      if (!v) return sig('neutral')
      const price = c[c.length - 1]!.close
      const pct = (price - v.lower) / (v.upper - v.lower)
      if (pct > 1) return sig('sell', `${(pct * 100).toFixed(0)}%B`)
      if (pct < 0) return sig('buy', `${(pct * 100).toFixed(0)}%B`)
      return sig('neutral', `${(pct * 100).toFixed(0)}%B`)
    },
    Preview: () => {
      const d = demo.sin(32, 1.5, 8, 50)
      return <SparkLine data={d} stroke="#f7a600" fill="#f7a600" bands={{ upper: d.map(v => v + 10), lower: d.map(v => v - 10) }} />
    },
  },
  {
    id: 'stoch', abbr: 'STC', name: 'Stochastic', cat: 'momentum',
    desc: 'Положение цены в диапазоне периода',
    about: 'Stochastic = (Close − Low(k)) / (High(k) − Low(k)) × 100. %K — быстрая линия, %D = SMA(%K, d) — сглаженная сигнальная. Зоны 80+ и 20−: перекупленность/перепроданность. Пересечение %K и %D в экстремальной зоне — сигнал входа. В сильных трендах подаёт много ложных сигналов.',
    defaults: { k: 14, d: 3, smooth: 3, tf: '1h' } as StochP,
    params: [
      { kind: 'number', key: 'k',      label: '%K период', hint: 'Число баров для диапазона High-Low. Стандарт: 14.' },
      { kind: 'number', key: 'd',      label: '%D период', hint: 'Сглаживание %K для получения сигнальной линии. Стандарт: 3.' },
      { kind: 'number', key: 'smooth', label: 'Сглаж.',    hint: 'Дополнительное сглаживание %K (медленный Stochastic). Стандарт: 3.' },
    ],
    compute: (p: StochP, c: Candle[]) => {
      if (c.length < p.k + p.d + 1) return sig('neutral')
      const v = last(Stochastic.calculate({ high: c.map(x => x.high), low: c.map(x => x.low), close: c.map(x => x.close), period: p.k, signalPeriod: p.d }))
      if (!v) return sig('neutral')
      if (v.k <= 20) return sig('buy', `K=${v.k.toFixed(1)}`)
      if (v.k >= 80) return sig('sell', `K=${v.k.toFixed(1)}`)
      return sig('neutral', `K=${v.k.toFixed(1)}`)
    },
    Preview: () => <SparkLine data={demo.sin(32, 3, 30, 50)} stroke="#b8c8ff" hLines={[{ v: 80, c: '#fca5a5' }, { v: 20, c: '#5be0a0' }]} />,
  },
  {
    id: 'atr', abbr: 'ATR', name: 'ATR', cat: 'volatility',
    desc: 'Средний истинный диапазон — мера волатильности',
    about: 'True Range = max(High−Low, |High−Close_prev|, |Low−Close_prev|). ATR — среднее TR за период. Не показывает направление, только размах движений. Применяется для расчёта стопов (Stop = вход ± 2×ATR) и оценки текущей волатильности. Высокий ATR — турбулентный рынок.',
    defaults: { period: 14, tf: '1h' } as AtrP,
    params: [
      { kind: 'number', key: 'period', label: 'Период', hint: 'Число баров для усреднения истинного диапазона. Стандарт: 14.' },
    ],
    compute: (p: AtrP, c: Candle[]) => {
      if (c.length < p.period + 1) return sig('neutral')
      const arr = ATR.calculate({ high: c.map(x => x.high), low: c.map(x => x.low), close: c.map(x => x.close), period: p.period })
      const v = last(arr)
      if (v === null) return sig('neutral')
      const avg = arr.length >= 20 ? arr.slice(-20).reduce((a, b) => a + b, 0) / 20 : v
      const ratio = v / avg
      if (ratio > 1.5) return sig('sell', `${v.toFixed(3)}↑`)
      if (ratio < 0.7) return sig('neutral', `${v.toFixed(3)}↓`)
      return sig('neutral', v.toFixed(3))
    },
    Preview: () => <SparkLine data={demo.sin(32, 4, 10, 30)} stroke="#f7a600" fill="#f7a600" />,
  },
  {
    id: 'adx', abbr: 'ADX', name: 'ADX', cat: 'trend',
    desc: 'Сила тренда независимо от направления',
    about: 'ADX (0–100) измеряет силу тренда: < 20 — флет или слабый тренд, 20–40 — формирующийся тренд, > 40 — сильный. +DI и −DI показывают давление покупателей/продавцов: +DI > −DI — восходящий тренд. ADX не указывает направление — только силу.',
    defaults: { period: 14, threshold: 25, mode: 'trend', tf: '1h' } as AdxP,
    params: [
      { kind: 'number',    key: 'period',    label: 'Период',  hint: 'Число баров для расчёта DI+ и DI−. Стандарт: 14.' },
      { kind: 'number',    key: 'threshold', label: 'Порог',   hint: 'ADX > порога — тренд. ADX < порога — боковик. Стандарт: 25.' },
      { kind: 'segmented', key: 'mode',      label: 'Режим',   hint: 'Тренд — сигнал когда рынок трендовый. Боковик — сигнал когда рынок в флете.', options: ['trend', 'flat'] as const },
    ],
    compute: (p: AdxP, c: Candle[]) => {
      if (c.length < p.period * 2) return sig('neutral')
      const v = last(ADX.calculate({ high: c.map(x => x.high), low: c.map(x => x.low), close: c.map(x => x.close), period: p.period }))
      if (!v) return sig('neutral')
      const isFlat = (p.mode ?? 'trend') === 'flat'
      if (isFlat) {
        // Flat mode: fire when ADX < threshold
        if (v.adx >= p.threshold) return sig('neutral', v.adx.toFixed(1))
        return sig(v.pdi > v.mdi ? 'buy' : 'sell', v.adx.toFixed(1))
      }
      // Trend mode (default): fire when ADX >= threshold
      if (v.adx >= p.threshold) return sig(v.pdi > v.mdi ? 'buy' : 'sell', v.adx.toFixed(1))
      return sig('neutral', v.adx.toFixed(1))
    },
    Preview: () => <SparkLine data={Array.from({ length: 32 }, (_, i) => 15 + Math.abs(Math.sin((i / 31) * Math.PI * 1.5)) * 40)} stroke="#5be0a0" fill="#5be0a0" hLines={[{ v: 25, c: '#7b8aa6' }]} />,
  },
  {
    id: 'ichi', abbr: 'ICH', name: 'Ichimoku', cat: 'trend',
    desc: 'Облако Ишимоку — комплексный тренд',
    about: 'Пять линий: Tenkan-sen (9), Kijun-sen (26), Senkou A/B (образуют облако Kumo), Chikou Span (лаг 26). Цена выше облака — бычья тенденция. Ниже — медвежья. Внутри облака — неопределённость. Kijun-sen — долгосрочный уровень баланса за 26 периодов.',
    defaults: { tenkan: 9, kijun: 26, senkou: 52, shift: 26, tf: '1h' } as IchiP,
    params: [
      { kind: 'number', key: 'tenkan', label: 'Tenkan',   hint: 'Период Tenkan-sen (линия конверсии). Стандарт: 9.' },
      { kind: 'number', key: 'kijun',  label: 'Kijun',   hint: 'Период Kijun-sen (базовая линия). Стандарт: 26.' },
      { kind: 'number', key: 'senkou', label: 'Senkou B', hint: 'Период Senkou B (нижняя граница Kumo). Стандарт: 52.' },
      { kind: 'number', key: 'shift',  label: 'Сдвиг',   hint: 'Смещение облака вперёд (проекция). Стандарт: 26.' },
    ],
    compute: (p: IchiP, c: Candle[]) => {
      if (c.length < p.senkou + p.shift) return sig('neutral')
      const v = last(IchimokuCloud.calculate({ high: c.map(x => x.high), low: c.map(x => x.low), conversionPeriod: p.tenkan, basePeriod: p.kijun, spanPeriod: p.senkou, displacement: p.shift }))
      if (!v) return sig('neutral')
      const price = c[c.length - 1]!.close
      const top = Math.max(v.spanA, v.spanB), bot = Math.min(v.spanA, v.spanB)
      if (price > top) return sig('buy', 'above')
      if (price < bot) return sig('sell', 'below')
      return sig('neutral', 'inside')
    },
    Preview: () => {
      const d = demo.up()
      return <SparkLine data={d} stroke="#5be0a0" fill="#5be0a0" bands={{ upper: d.map(v => v + 8), lower: d.map(v => v - 6) }} />
    },
  },
  {
    id: 'vwap', abbr: 'VWP', name: 'VWAP', cat: 'volume',
    desc: 'Средневзвешенная по объёму цена',
    about: 'VWAP = Σ(TP × Volume) / ΣVolume, где TP = (High+Low+Close)/3. Перезапускается каждую сессию. Институциональные трейдеры ориентируются на VWAP при исполнении крупных ордеров: покупка ниже VWAP — выгодная цена, продажа выше — дорого. Хорош как фильтр, не как самостоятельный сигнал.',
    defaults: { anchor: 'session', tf: '1h' } as VwapP,
    params: [
      { kind: 'segmented', key: 'anchor', label: 'Якорь', hint: 'Точка сброса VWAP: session — каждая сессия, day — каждый день, week — каждую неделю.', options: ['session', 'day', 'week'] },
    ],
    compute: (_p: VwapP, c: Candle[]) => {
      if (!c.length) return sig('neutral')
      let tvp = 0, tv = 0
      for (const x of c) { const tp = (x.high + x.low + x.close) / 3; tvp += tp * x.volume; tv += x.volume }
      const vwap = tv ? tvp / tv : 0
      const price = c[c.length - 1]!.close
      return sig(price > vwap ? 'buy' : 'sell', vwap.toFixed(2))
    },
    Preview: () => <SparkLine data={demo.up()} stroke="#d8a4ff" fill="#d8a4ff" />,
  },
  {
    id: 'obv', abbr: 'OBV', name: 'OBV', cat: 'volume',
    desc: 'Балансовый объём — кумулятивный поток',
    about: 'OBV накапливает объём: прибавляет на бычьей свече, вычитает на медвежьей. Рост OBV при консолидации цены — накопление позиции крупными игроками (bullish). Снижение OBV при росте цены — распределение (bearish divergence). Один из лучших объёмных индикаторов.',
    defaults: { tf: '1h' },
    params: [],
    compute: (_p, c: Candle[]) => {
      if (c.length < 2) return sig('neutral')
      const arr = OBV.calculate({ close: c.map(x => x.close), volume: c.map(x => x.volume) })
      const v = last(arr), prev = arr[arr.length - 2]
      if (v === null || prev === undefined) return sig('neutral')
      return sig(v > prev ? 'buy' : 'sell', v > prev ? 'rising' : 'falling')
    },
    Preview: () => <SparkLine data={demo.up()} stroke="#d8a4ff" fill="#d8a4ff" />,
  },
  {
    id: 'cci', abbr: 'CCI', name: 'CCI', cat: 'momentum',
    desc: 'Индекс товарного канала — отклонение от средней',
    about: 'CCI = (TP − SMA(TP)) / (0.015 × среднее отклонение). Около 75% времени значения попадают в диапазон ±100 (случайные колебания). Выход за ±100 — аномальное движение. Дивергенция CCI с ценой — один из надёжных разворотных сигналов.',
    defaults: { period: 20, upper: 100, lower: -100, source: 'hl2', tf: '1h' } as CciP,
    params: [
      { kind: 'number', key: 'period', label: 'Период',      hint: 'Число баров для SMA и среднего отклонения. Стандарт: 20.' },
      { kind: 'number', key: 'upper',  label: 'Верх. порог', hint: 'Порог перекупленности. При CCI > upper — сигнал Sell. Стандарт: +100.', step: 10 },
      { kind: 'number', key: 'lower',  label: 'Нижн. порог', hint: 'Порог перепроданности. При CCI < lower — сигнал Buy. Стандарт: −100.', step: 10 },
    ],
    compute: (p: CciP, c: Candle[]) => {
      if (c.length < p.period) return sig('neutral')
      const v = last(CCI.calculate({ high: c.map(x => x.high), low: c.map(x => x.low), close: c.map(x => x.close), period: p.period }))
      if (v === null) return sig('neutral')
      if (v > p.upper) return sig('sell', `+${v.toFixed(0)}`)
      if (v < p.lower) return sig('buy', v.toFixed(0))
      return sig('neutral', v.toFixed(0))
    },
    Preview: () => <SparkLine data={demo.sin(32, 2, 40, 50)} stroke="#b8c8ff" hLines={[{ v: 100, c: '#fca5a5' }, { v: 0, c: '#7b8aa6' }, { v: -50, c: '#5be0a0' }]} />,
  },
  {
    id: 'wpr', abbr: '%R', name: 'Williams %R', cat: 'momentum',
    desc: 'Положение закрытия в диапазоне периода',
    about: 'W%R = (High(N) − Close) / (High(N) − Low(N)) × −100. Шкала 0 до −100. Зона 0…−20: перекупленность (Sell). Зона −80…−100: перепроданность (Buy). Структурно аналогичен %K Стохастика, но не сглажен. Эффективен для поиска разворотов в боковых рынках.',
    defaults: { period: 14, upper: -20, lower: -80, tf: '1h' } as WprP,
    params: [
      { kind: 'number', key: 'period', label: 'Период',    hint: 'Число баров для диапазона High-Low. Стандарт: 14.' },
      { kind: 'number', key: 'upper',  label: 'Перекупл.', hint: 'Порог перекупленности (значения около 0). Стандарт: −20.', step: 5 },
      { kind: 'number', key: 'lower',  label: 'Перепрод.', hint: 'Порог перепроданности (значения около −100). Стандарт: −80.', step: 5 },
    ],
    compute: (p: WprP, c: Candle[]) => {
      if (c.length < p.period) return sig('neutral')
      const v = last(WilliamsR.calculate({ high: c.map(x => x.high), low: c.map(x => x.low), close: c.map(x => x.close), period: p.period }))
      if (v === null) return sig('neutral')
      if (v > p.upper) return sig('sell', v.toFixed(1))
      if (v < p.lower) return sig('buy', v.toFixed(1))
      return sig('neutral', v.toFixed(1))
    },
    Preview: () => <SparkLine data={demo.sin(32, 2.5, 30, 50)} stroke="#b8c8ff" hLines={[{ v: 80, c: '#5be0a0' }, { v: 20, c: '#fca5a5' }]} />,
  },
  {
    id: 'psar', abbr: 'SAR', name: 'Parabolic SAR', cat: 'trend',
    desc: 'Параболическая остановка и разворот',
    about: 'SAR следует за ценой, постепенно ускоряясь: с каждым новым экстремумом Acceleration Factor (AF) увеличивается на шаг до максимума. При касании цены SAR перебрасывается на другую сторону и сигнализирует о смене тренда. Меньший шаг = медленнее, меньше разворотов.',
    defaults: { step: 0.02, max: 0.20, tf: '1h' } as PsarP,
    params: [
      { kind: 'number', key: 'step', label: 'Шаг',  hint: 'Шаг ускорения AF (Acceleration Factor). Чем больше — тем быстрее SAR приближается к цене. Стандарт: 0.02.', step: 0.01, decimals: 2 },
      { kind: 'number', key: 'max',  label: 'Макс', hint: 'Максимальное значение AF. Ограничивает ускорение SAR. Стандарт: 0.20.', step: 0.05, decimals: 2 },
    ],
    compute: (p: PsarP, c: Candle[]) => {
      if (c.length < 2) return sig('neutral')
      const v = last(PSAR.calculate({ high: c.map(x => x.high), low: c.map(x => x.low), step: p.step, max: p.max }))
      if (v === null) return sig('neutral')
      const price = c[c.length - 1]!.close
      return sig(price > v ? 'buy' : 'sell', v.toFixed(2))
    },
    Preview: () => <SparkLine data={demo.up()} stroke="#5be0a0" fill="#5be0a0" />,
  },
  {
    id: 'mfi', abbr: 'MFI', name: 'MFI', cat: 'volume',
    desc: 'Индекс денежного потока — RSI с учётом объёма',
    about: 'Money Ratio = Позитивный MF / Негативный MF, где MF = TP × Volume. MFI = 100 − 100/(1+MR). Аналог RSI, но учитывает объём торгов. Сигналы более значимы при высоком объёме. Дивергенция MFI с ценой в экстремальных зонах — сильный разворотный сигнал.',
    defaults: { period: 14, upper: 80, lower: 20, tf: '1h' } as MfiP,
    params: [
      { kind: 'number', key: 'period', label: 'Период',    hint: 'Число баров для расчёта денежного потока. Стандарт: 14.' },
      { kind: 'number', key: 'upper',  label: 'Перекупл.', hint: 'Порог перекупленности. При MFI ≥ upper — сигнал Sell. Стандарт: 80.' },
      { kind: 'number', key: 'lower',  label: 'Перепрод.', hint: 'Порог перепроданности. При MFI ≤ lower — сигнал Buy. Стандарт: 20.' },
    ],
    compute: (p: MfiP, c: Candle[]) => {
      if (c.length < p.period + 1) return sig('neutral')
      const v = last(MFI.calculate({ high: c.map(x => x.high), low: c.map(x => x.low), close: c.map(x => x.close), volume: c.map(x => x.volume), period: p.period }))
      if (v === null) return sig('neutral')
      if (v >= p.upper) return sig('sell', v.toFixed(1))
      if (v <= p.lower) return sig('buy', v.toFixed(1))
      return sig('neutral', v.toFixed(1))
    },
    Preview: () => <SparkLine data={demo.sin(32, 2, 25, 50)} stroke="#d8a4ff" hLines={[{ v: 80, c: '#fca5a5' }, { v: 20, c: '#5be0a0' }]} />,
  },
  {
    id: 'vol', abbr: 'VOL', name: 'Volume', cat: 'volume',
    desc: 'Объём торгов с MA-сглаживанием',
    about: 'Сравнивает текущий объём с его скользящей средней. Объём > 1.5× MA — аномальная активность, часто сопровождающая пробои уровней или развороты. Бычья свеча с аномальным объёмом → Buy, медвежья → Sell. Используйте в комбинации с уровнями или пробоями.',
    defaults: { maPeriod: 20, tf: '1h' } as VolP,
    params: [
      { kind: 'number', key: 'maPeriod', label: 'MA период', hint: 'Период скользящей средней объёма для сравнения. Стандарт: 20.' },
    ],
    compute: (p: VolP, c: Candle[]) => {
      if (c.length < p.maPeriod + 1) return sig('neutral')
      const vols = c.map(x => x.volume)
      const ma = last(SMA.calculate({ values: vols, period: p.maPeriod }))
      if (!ma) return sig('neutral')
      const cur = vols[vols.length - 1]!, ratio = cur / ma
      const last_ = c[c.length - 1]!
      const state: SignalState = ratio > 1.5 ? (last_.close >= last_.open ? 'buy' : 'sell') : 'neutral'
      return sig(state, `${ratio.toFixed(1)}×`)
    },
    Preview: () => <SparkBars data={demo.bars()} color="#d8a4ff" />,
  },
  {
    id: 'roc', abbr: 'ROC', name: 'Rate of Change', cat: 'momentum',
    desc: 'Скорость изменения цены за период',
    about: 'ROC = (Close − Close[N]) / Close[N] × 100%. Осциллятор без ограничений. Пересечение нуля снизу вверх — подтверждение бычьего разворота. Ускоряющийся ROC → тренд набирает силу. Замедляющийся ROC → тренд слабеет. Хорош для подтверждения импульса.',
    defaults: { period: 12, source: 'close', tf: '1h' } as RocP,
    params: [
      { kind: 'number', key: 'period', label: 'Период', hint: 'Число баров назад для сравнения с текущей ценой. Стандарт: 12.' },
    ],
    compute: (p: RocP, c: Candle[]) => {
      if (c.length < p.period + 1) return sig('neutral')
      const v = last(ROC.calculate({ values: src(c, p.source), period: p.period }))
      if (v === null) return sig('neutral')
      return sig(v > 0 ? 'buy' : v < 0 ? 'sell' : 'neutral', `${v > 0 ? '+' : ''}${v.toFixed(2)}%`)
    },
    Preview: () => <SparkLine data={demo.sin(32, 1.6, 30, 50)} stroke="#b8c8ff" hLines={[{ v: 50, c: '#7b8aa6' }]} />,
  },
  {
    id: 'st', abbr: 'ST', name: 'SuperTrend', cat: 'trend',
    desc: 'Тренд-фильтр на основе ATR',
    about: 'Строит верхнюю/нижнюю границы: Mid ± ATR×mult. Пока цена выше нижней линии — тренд бычий. При пробое вниз — тренд меняется на медвежий. Множитель критичен: 1.5–2 = много сигналов, 3–4 = редкие и надёжные. По умолчанию 3.0 — хороший баланс.',
    defaults: { atr: 10, mult: 3.0, tf: '1h' } as StP,
    params: [
      { kind: 'number', key: 'atr',  label: 'ATR период',  hint: 'Период для расчёта Average True Range. Стандарт: 10.' },
      { kind: 'number', key: 'mult', label: 'Множитель',   hint: 'Коэффициент ATR для ширины канала. Меньше → чаще флипы, больше → реже, надёжнее. Стандарт: 3.0.', step: 0.1, decimals: 1 },
    ],
    compute: (p: StP, c: Candle[]) => {
      if (c.length < p.atr + 1) return sig('neutral')
      const { state, val } = stDir(c, p.atr, p.mult)
      return sig(state, val.toString())
    },
    Preview: () => <SparkLine data={demo.up()} stroke="#5be0a0" fill="#5be0a0" />,
  },
  {
    id: 'kc', abbr: 'KC', name: 'Keltner Channels', cat: 'volatility',
    desc: 'Канал ATR вокруг EMA',
    about: 'Канал = EMA ± ATR×N. В отличие от Bollinger Bands использует ATR вместо σ — менее чувствителен к разовым выбросам. Сжатие Keltner при расширении BB («BB inside KC») — Squeeze Momentum, предвещающий резкое движение. Прорыв за KC подтверждает силу импульса.',
    defaults: { period: 20, atr: 10, mult: 2.0, tf: '1h' } as KcP,
    params: [
      { kind: 'number', key: 'period', label: 'EMA период',  hint: 'Период центральной EMA канала. Стандарт: 20.' },
      { kind: 'number', key: 'atr',    label: 'ATR период',  hint: 'Период для расчёта ATR (ширина канала). Стандарт: 10.' },
      { kind: 'number', key: 'mult',   label: 'Множитель',   hint: 'Коэффициент ATR для ширины полос. Больше → шире канал, меньше сигналов. Стандарт: 2.0.', step: 0.1, decimals: 1 },
    ],
    compute: (p: KcP, c: Candle[]) => {
      if (c.length < Math.max(p.period, p.atr) + 1) return sig('neutral')
      const ema = last(EMA.calculate({ values: c.map(x => x.close), period: p.period }))
      const atr = last(ATR.calculate({ high: c.map(x => x.high), low: c.map(x => x.low), close: c.map(x => x.close), period: p.atr }))
      if (!ema || !atr) return sig('neutral')
      const price = c[c.length - 1]!.close
      if (price > ema + p.mult * atr) return sig('buy', 'above')
      if (price < ema - p.mult * atr) return sig('sell', 'below')
      return sig('neutral', 'mid')
    },
    Preview: () => {
      const d = demo.up()
      return <SparkLine data={d} stroke="#f7a600" bands={{ upper: d.map(v => v + 10), lower: d.map(v => v - 10) }} />
    },
  },
  {
    id: 'ao', abbr: 'AO', name: 'Awesome Oscillator', cat: 'momentum',
    desc: 'Разница SMA(5) и SMA(34) по HL2',
    about: 'AO = SMA(HL2, fast) − SMA(HL2, slow). Стандарт: 5/34. Пересечение нулевой линии → смена тренда. Паттерн «Блюдце» (3 бара с минимумом посередине) → продолжение тренда. «Twin Peaks» (два пика с расхождением выше/ниже нуля) → разворот. Простой и наглядный импульсный осциллятор.',
    defaults: { fast: 5, slow: 34, tf: '1h' } as AoP,
    params: [
      { kind: 'number', key: 'fast', label: 'Fast', hint: 'Период быстрой SMA по HL2. Стандарт: 5.' },
      { kind: 'number', key: 'slow', label: 'Slow', hint: 'Период медленной SMA по HL2. Стандарт: 34.' },
    ],
    compute: (p: AoP, c: Candle[]) => {
      if (c.length < p.slow) return sig('neutral')
      const hl2 = c.map(x => (x.high + x.low) / 2)
      const fast = SMA.calculate({ values: hl2, period: p.fast })
      const slow = SMA.calculate({ values: hl2, period: p.slow })
      const off = hl2.length - slow.length
      const ao = slow.map((s, i) => fast[i + off]! - s)
      const v = last(ao)
      if (v === null) return sig('neutral')
      return sig(v > 0 ? 'buy' : 'sell', v > 0 ? `+${v.toFixed(2)}` : v.toFixed(2))
    },
    Preview: () => <SparkLine data={demo.sin(32, 2.2, 25, 50)} stroke="#b8c8ff" hLines={[{ v: 50, c: '#7b8aa6' }]} />,
  },
]

export { SOURCES, TFS }
