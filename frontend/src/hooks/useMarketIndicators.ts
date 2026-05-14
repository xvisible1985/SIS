import { useEffect, useRef, useState } from 'react'
import {
  RSI, MACD, EMA, SMA, BollingerBands, Stochastic,
  ADX, CCI, ATR, OBV, WilliamsR, MFI, PSAR, IchimokuCloud
} from 'technicalindicators'

interface Candle {
  time: number
  open: number
  high: number
  low: number
  close: number
  volume: number
}

export interface IndicatorValue {
  name: string
  fullName: string
  value: number | null
  status: 'bullish' | 'bearish' | 'overbought' | 'oversold' | 'neutral' | 'volatile' | 'squeeze'
  description: string
  icon: string
}

export interface DivergenceResult {
  indicator: string
  type: 'bullish' | 'bearish'
  price1: number
  price2: number
  ind1: number
  ind2: number
  time1: number
  time2: number
  barsAgo: number
}

export interface MultiDivergenceSignal {
  divergences: DivergenceResult[]
}

export interface SignalEvent {
  time: number
  ratio: number
  direction: 'bullish' | 'bearish'
  price: number
}

export interface VolumeSpikeSignal {
  isSpike: boolean
  currentVolume: number
  avgVolume: number
  ratio: number
  direction: 'bullish' | 'bearish' | 'neutral'
  history: SignalEvent[]
}

export interface AtrBreakoutSignal {
  isBreakout: boolean
  currentMove: number
  atr: number
  ratio: number
  direction: 'bullish' | 'bearish' | 'neutral'
  history: SignalEvent[]
}

export interface SupertrendPoint {
  time: number
  value: number
  direction: 'bullish' | 'bearish'
  price: number
}

export interface SupertrendSignal {
  direction: 'bullish' | 'bearish'
  value: number
  price: number
  distancePct: number
  crossovers: SupertrendPoint[]
  period: number
  multiplier: number
}

export type IndicatorMarker = { time: number; direction: 'bullish' | 'bearish'; label: string }

const HISTORY_LIMIT = 500
const CANDLE_TTL = 60_000
const CALC_THROTTLE_MS = 1000

// Module-level candle cache (persists across re-renders)
const candleCache: Record<string, { candles: Candle[]; ts: number }> = {}

export function toBybitInterval(interval: string): string {
  const map: Record<string, string> = {
    '1m': '1', '5m': '5', '15m': '15', '30m': '30',
    '1h': '60', '4h': '240', '1d': 'D',
  }
  return map[interval] ?? '60'
}

function last<T>(arr: T[]): T | null {
  return arr.length > 0 ? arr[arr.length - 1]! : null
}

function determineStatus(
  name: string,
  value: number | null,
  extra?: Record<string, number | null | undefined>,
): { status: IndicatorValue['status']; description: string } {
  if (value === null && name !== 'Ichimoku') return { status: 'neutral', description: '—' }
  switch (name) {
    case 'RSI':
      if (value! >= 70) return { status: 'overbought', description: 'Зона перекупленности (> 70)' }
      if (value! <= 30) return { status: 'oversold', description: 'Зона перепроданности (< 30)' }
      return { status: 'neutral', description: 'Нейтральная зона (30–70)' }
    case 'MACD':
      return (extra?.histogram ?? 0) > 0
        ? { status: 'bullish', description: 'Бычье пересечение сигнальной линии' }
        : { status: 'bearish', description: 'Медвежье пересечение сигнальной линии' }
    case 'EMA':
      return (extra?.price ?? 0) > value!
        ? { status: 'bullish', description: 'Цена выше EMA(20) — восходящий тренд' }
        : { status: 'bearish', description: 'Цена ниже EMA(20) — нисходящий тренд' }
    case 'BB':
      if (value! > 1) return { status: 'overbought', description: '%B > 1 — цена выше верхней полосы' }
      if (value! < 0) return { status: 'oversold', description: '%B < 0 — цена ниже нижней полосы' }
      return { status: 'neutral', description: '%B в пределах полос Боллинджера' }
    case 'Stoch':
      if (value! >= 80) return { status: 'overbought', description: `K = ${value} — зона перекупленности` }
      if (value! <= 20) return { status: 'oversold', description: `K = ${value} — зона перепроданности` }
      return { status: 'neutral', description: `K = ${value} — нейтральная зона` }
    case 'ADX':
      if (value! > 25) return {
        status: (extra?.plusDI ?? 0) > (extra?.minusDI ?? 0) ? 'bullish' : 'bearish',
        description: 'Сильный тренд (ADX > 25)',
      }
      return { status: 'neutral', description: 'Слабый тренд (ADX < 25)' }
    case 'CCI':
      if (value! > 100) return { status: 'overbought', description: 'Зона перекупленности (> 100)' }
      if (value! < -100) return { status: 'oversold', description: 'Зона перепроданности (< -100)' }
      return { status: 'neutral', description: 'Нейтральная зона (-100..100)' }
    case 'ATR': {
      const avg = extra?.atrAvg ?? null
      if (avg && value !== null) {
        const ratio = value / avg
        if (ratio > 1.5) return { status: 'volatile', description: `Высокая волатильность (+${((ratio - 1) * 100).toFixed(0)}% от нормы)` }
        if (ratio < 0.7) return { status: 'squeeze', description: `Сжатие волатильности (${((1 - ratio) * 100).toFixed(0)}% ниже нормы)` }
      }
      return { status: 'neutral', description: 'Нормальная волатильность' }
    }
    case 'OBV':
      if ((extra?.prevObv ?? 0) !== 0) {
        return value! > (extra?.prevObv ?? 0)
          ? { status: 'bullish', description: 'Объём подтверждает рост цены' }
          : { status: 'bearish', description: 'Объём подтверждает снижение цены' }
      }
      return { status: 'neutral', description: 'Объёмный индикатор' }
    case 'Williams %R':
      if (value! > -20) return { status: 'overbought', description: 'Зона перекупленности (> -20)' }
      if (value! < -80) return { status: 'oversold', description: 'Зона перепроданности (< -80)' }
      return { status: 'neutral', description: 'Нейтральная зона (-80..-20)' }
    case 'MFI':
      if (value! >= 80) return { status: 'overbought', description: 'Зона перекупленности (> 80)' }
      if (value! <= 20) return { status: 'oversold', description: 'Зона перепроданности (< 20)' }
      return { status: 'neutral', description: 'Нейтральная зона (20–80)' }
    case 'SAR':
      return (extra?.price ?? 0) > value!
        ? { status: 'bullish', description: 'SAR ниже цены — восходящий тренд' }
        : { status: 'bearish', description: 'SAR выше цены — нисходящий тренд' }
    case 'SMA':
      return (extra?.price ?? 0) > value!
        ? { status: 'bullish', description: 'Цена выше SMA(50)' }
        : { status: 'bearish', description: 'Цена ниже SMA(50)' }
    case 'Ichimoku':
      if ((extra?.price ?? 0) > (extra?.cloud ?? 0)) return { status: 'bullish', description: 'Цена выше облака — бычий сигнал' }
      if ((extra?.price ?? 0) < (extra?.cloudB ?? 0)) return { status: 'bearish', description: 'Цена ниже облака — медвежий сигнал' }
      return { status: 'neutral', description: 'Цена внутри облака — консолидация' }
    case 'Фандинг':
      if (value! > 0.01) return { status: 'overbought', description: 'Высокий фандинг — лонги платят шортам' }
      if (value! < -0.01) return { status: 'oversold', description: 'Отрицательный фандинг — шорты платят лонгам' }
      return { status: 'neutral', description: 'Нейтральный фандинг' }
    default:
      return { status: 'neutral', description: '' }
  }
}

function findSwings(values: number[], lookback = 5) {
  const result: { index: number; value: number; type: 'high' | 'low' }[] = []
  for (let i = lookback; i < values.length - lookback; i++) {
    const slice = values.slice(i - lookback, i + lookback + 1)
    const v = values[i]!
    if (v === Math.max(...slice)) result.push({ index: i, value: v, type: 'high' })
    else if (v === Math.min(...slice)) result.push({ index: i, value: v, type: 'low' })
  }
  return result
}

function detectDivergence(
  closes: number[], times: number[], indValues: number[],
  indName: string, lookback = 5, maxGap = 60,
): DivergenceResult[] {
  const results: DivergenceResult[] = []
  const offset = closes.length - indValues.length
  const alignedCloses = closes.slice(offset)
  const alignedTimes = times.slice(offset)
  const priceSwings = findSwings(alignedCloses, lookback)
  const indSwings = findSwings(indValues, lookback)
  const highs = priceSwings.filter(s => s.type === 'high').slice(-6)
  const lows = priceSwings.filter(s => s.type === 'low').slice(-6)

  for (let i = highs.length - 1; i > 0; i--) {
    const p2 = highs[i]!, p1 = highs[i - 1]!
    if (p2.index - p1.index > maxGap || p2.value <= p1.value) continue
    const iH2 = indSwings.filter(s => s.type === 'high' && Math.abs(s.index - p2.index) <= lookback * 2)
    const iH1 = indSwings.filter(s => s.type === 'high' && Math.abs(s.index - p1.index) <= lookback * 2)
    if (!iH2.length || !iH1.length) continue
    const iv2 = iH2.reduce((a, b) => Math.abs(a.index - p2.index) < Math.abs(b.index - p2.index) ? a : b)
    const iv1 = iH1.reduce((a, b) => Math.abs(a.index - p1.index) < Math.abs(b.index - p1.index) ? a : b)
    if (iv2.value < iv1.value) {
      results.push({ indicator: indName, type: 'bearish', price1: p1.value, price2: p2.value, ind1: +iv1.value.toFixed(3), ind2: +iv2.value.toFixed(3), time1: alignedTimes[p1.index]!, time2: alignedTimes[p2.index]!, barsAgo: alignedCloses.length - 1 - p2.index })
      break
    }
  }
  for (let i = lows.length - 1; i > 0; i--) {
    const p2 = lows[i]!, p1 = lows[i - 1]!
    if (p2.index - p1.index > maxGap || p2.value >= p1.value) continue
    const iL2 = indSwings.filter(s => s.type === 'low' && Math.abs(s.index - p2.index) <= lookback * 2)
    const iL1 = indSwings.filter(s => s.type === 'low' && Math.abs(s.index - p1.index) <= lookback * 2)
    if (!iL2.length || !iL1.length) continue
    const iv2 = iL2.reduce((a, b) => Math.abs(a.index - p2.index) < Math.abs(b.index - p2.index) ? a : b)
    const iv1 = iL1.reduce((a, b) => Math.abs(a.index - p1.index) < Math.abs(b.index - p1.index) ? a : b)
    if (iv2.value > iv1.value) {
      results.push({ indicator: indName, type: 'bullish', price1: p1.value, price2: p2.value, ind1: +iv1.value.toFixed(3), ind2: +iv2.value.toFixed(3), time1: alignedTimes[p1.index]!, time2: alignedTimes[p2.index]!, barsAgo: alignedCloses.length - 1 - p2.index })
      break
    }
  }
  return results
}

function calcSupertrend(highs: number[], lows: number[], closes: number[], times: number[], period = 10, multiplier = 3): SupertrendPoint[] {
  const atrArr = ATR.calculate({ high: highs, low: lows, close: closes, period })
  if (!atrArr.length) return []
  const offset = closes.length - atrArr.length
  const result: SupertrendPoint[] = []
  let fub = 0, flb = 0
  let prevDir: 'bullish' | 'bearish' = 'bearish'
  for (let i = 0; i < atrArr.length; i++) {
    const idx = i + offset
    const high = highs[idx]!, low = lows[idx]!, close = closes[idx]!, atr = atrArr[i]!
    const mid = (high + low) / 2
    const bub = mid + multiplier * atr
    const blb = mid - multiplier * atr
    if (i === 0) { fub = bub; flb = blb; result.push({ time: times[idx]!, value: fub, direction: 'bearish', price: close }); continue }
    const prevClose = closes[idx - 1]!
    fub = (bub < fub || prevClose > fub) ? bub : fub
    flb = (blb > flb || prevClose < flb) ? blb : flb
    let dir: 'bullish' | 'bearish', val: number
    if (prevDir === 'bearish') {
      dir = close > fub ? 'bullish' : 'bearish'
      val = dir === 'bullish' ? flb : fub
    } else {
      dir = close < flb ? 'bearish' : 'bullish'
      val = dir === 'bearish' ? fub : flb
    }
    prevDir = dir
    result.push({ time: times[idx]!, value: val, direction: dir, price: close })
  }
  return result
}

function calculateAll(candles: Candle[], fundingRate: number | null) {
  if (candles.length < 52) return null
  const closes = candles.map(x => x.close)
  const highs = candles.map(x => x.high)
  const lows = candles.map(x => x.low)
  const volumes = candles.map(x => x.volume)
  const times = candles.map(x => x.time)
  const price = closes[closes.length - 1]!

  const rsiArr = RSI.calculate({ values: closes, period: 14 })
  const rsiVal = last(rsiArr)
  const macdArr = MACD.calculate({ values: closes, fastPeriod: 12, slowPeriod: 26, signalPeriod: 9, SimpleMAOscillator: false, SimpleMASignal: false })
  const macdVal = last(macdArr)
  const emaArr = EMA.calculate({ values: closes, period: 20 })
  const emaVal = last(emaArr)
  const bbArr = BollingerBands.calculate({ values: closes, period: 20, stdDev: 2 })
  const bbVal = last(bbArr)
  const bbPct = bbVal ? +((price - bbVal.lower) / (bbVal.upper - bbVal.lower)).toFixed(3) : null
  const stochArr = Stochastic.calculate({ high: highs, low: lows, close: closes, period: 14, signalPeriod: 3 })
  const stochVal = last(stochArr)
  const stochK = stochVal ? +stochVal.k.toFixed(1) : null
  const adxArr = ADX.calculate({ high: highs, low: lows, close: closes, period: 14 })
  const adxVal = last(adxArr)
  const cciArr = CCI.calculate({ high: highs, low: lows, close: closes, period: 20 })
  const cciVal = last(cciArr)
  const atrArr = ATR.calculate({ high: highs, low: lows, close: closes, period: 14 })
  const atrVal = last(atrArr)
  const ATR_AVG_PERIOD = 20
  const atrAvg = atrArr.length >= ATR_AVG_PERIOD ? atrArr.slice(-ATR_AVG_PERIOD).reduce((a, b) => a + b, 0) / ATR_AVG_PERIOD : null
  const obvArr = OBV.calculate({ close: closes, volume: volumes })
  const obvVal = last(obvArr)
  const prevObvVal = obvArr.length > 1 ? obvArr[obvArr.length - 2]! : null
  const wrArr = WilliamsR.calculate({ high: highs, low: lows, close: closes, period: 14 })
  const wrVal = last(wrArr)
  const mfiArr = MFI.calculate({ high: highs, low: lows, close: closes, volume: volumes, period: 14 })
  const mfiVal = last(mfiArr)
  const sarArr = PSAR.calculate({ high: highs, low: lows, step: 0.02, max: 0.2 })
  const sarVal = last(sarArr)
  const smaArr = SMA.calculate({ values: closes, period: 50 })
  const smaVal = last(smaArr)
  const ichiArr = IchimokuCloud.calculate({ high: highs, low: lows, conversionPeriod: 9, basePeriod: 26, spanPeriod: 52, displacement: 26 })
  const ichiVal = last(ichiArr)
  const cloudTop = ichiVal ? Math.max(ichiVal.spanA, ichiVal.spanB) : null
  const cloudBot = ichiVal ? Math.min(ichiVal.spanA, ichiVal.spanB) : null

  const raw = [
    { name: 'RSI', fullName: 'Relative Strength Index', value: rsiVal !== null ? +rsiVal.toFixed(2) : null, icon: 'activity' },
    { name: 'MACD', fullName: 'Moving Average Convergence/Divergence', value: macdVal ? +(macdVal.histogram?.toFixed(4) ?? 0) : null, icon: 'trending-up', extra: { histogram: macdVal?.histogram ?? null } },
    { name: 'EMA', fullName: 'Exponential Moving Average', value: emaVal !== null ? +emaVal.toFixed(2) : null, icon: 'trending-up', extra: { price } },
    { name: 'BB', fullName: 'Bollinger Bands', value: bbPct, icon: 'git-branch' },
    { name: 'Stoch', fullName: 'Stochastic Oscillator', value: stochK, icon: 'activity' },
    { name: 'ADX', fullName: 'Average Directional Index', value: adxVal ? +adxVal.adx.toFixed(1) : null, icon: 'bar-chart-2', extra: { plusDI: adxVal?.pdi ?? null, minusDI: adxVal?.mdi ?? null } },
    { name: 'CCI', fullName: 'Commodity Channel Index', value: cciVal !== null ? +cciVal.toFixed(1) : null, icon: 'trending-down' },
    { name: 'ATR', fullName: 'Average True Range', value: atrVal !== null ? +atrVal.toFixed(3) : null, icon: 'bar-chart-2', extra: { atrAvg } },
    { name: 'OBV', fullName: 'On Balance Volume', value: obvVal !== null ? Math.round(obvVal) : null, icon: 'trending-up', extra: { prevObv: prevObvVal } },
    { name: 'Williams %R', fullName: 'Williams Percent Range', value: wrVal !== null ? +wrVal.toFixed(1) : null, icon: 'percent' },
    { name: 'MFI', fullName: 'Money Flow Index', value: mfiVal !== null ? +mfiVal.toFixed(1) : null, icon: 'dollar-sign' },
    { name: 'SAR', fullName: 'Parabolic SAR', value: sarVal !== null ? +sarVal.toFixed(2) : null, icon: 'circle-dot', extra: { price } },
    { name: 'SMA', fullName: 'Simple Moving Average', value: smaVal !== null ? +smaVal.toFixed(2) : null, icon: 'minus', extra: { price } },
    { name: 'Ichimoku', fullName: 'Ichimoku Cloud', value: null, icon: 'layers', extra: { price, cloud: cloudTop, cloudB: cloudBot } },
    { name: 'Фандинг', fullName: 'Funding Rate', value: fundingRate, icon: 'percent' },
  ]

  const indicators: IndicatorValue[] = raw.map(r => {
    const { status, description } = determineStatus(r.name, r.value, r.extra)
    return { name: r.name, fullName: r.fullName, value: r.value, status, description, icon: r.icon }
  })

  // Supertrend
  const stPoints = calcSupertrend(highs, lows, closes, times, 10, 3)
  let supertrend: SupertrendSignal | null = null
  if (stPoints.length > 0) {
    const stLast = stPoints[stPoints.length - 1]!
    const crossovers: SupertrendPoint[] = []
    for (let i = stPoints.length - 1; i > 0 && crossovers.length < 8; i--) {
      if (stPoints[i]!.direction !== stPoints[i - 1]!.direction) crossovers.push(stPoints[i]!)
    }
    supertrend = { direction: stLast.direction, value: +stLast.value.toFixed(2), price: stLast.price, distancePct: +Math.abs((stLast.price - stLast.value) / stLast.price * 100).toFixed(2), crossovers, period: 10, multiplier: 3 }
  }

  // Volume Spike
  const VOL_PERIOD = 20
  let volumeSpike: VolumeSpikeSignal | null = null
  if (volumes.length > VOL_PERIOD) {
    const currentVol = volumes[volumes.length - 1]!
    const currentOpen = candles[candles.length - 1]!.open
    const avgVol = volumes.slice(-VOL_PERIOD - 1, -1).reduce((a, b) => a + b, 0) / VOL_PERIOD
    const ratio = currentVol / avgVol
    const direction = price >= currentOpen ? 'bullish' : 'bearish'
    const history: SignalEvent[] = []
    for (let i = candles.length - 2; i > VOL_PERIOD && history.length < 8; i--) {
      const vol = volumes[i]!, avg = volumes.slice(i - VOL_PERIOD, i).reduce((a, b) => a + b, 0) / VOL_PERIOD
      const r = vol / avg
      if (r >= 1.5) history.push({ time: candles[i]!.time, ratio: +r.toFixed(2), direction: candles[i]!.close >= candles[i]!.open ? 'bullish' : 'bearish', price: candles[i]!.close })
    }
    volumeSpike = { isSpike: ratio >= 2.0, currentVolume: Math.round(currentVol), avgVolume: Math.round(avgVol), ratio: +ratio.toFixed(2), direction, history }
  }

  // ATR Breakout
  let atrBreakout: AtrBreakoutSignal | null = null
  if (atrArr.length > 1 && candles.length > 1) {
    const currentAtr = atrArr[atrArr.length - 1]!
    const prevClose = closes[closes.length - 2]!
    const currentMove = Math.abs(price - prevClose)
    const ratio = currentMove / currentAtr
    const direction = price >= prevClose ? 'bullish' : 'bearish'
    const history: SignalEvent[] = []
    const atrOffset = closes.length - atrArr.length
    for (let i = atrArr.length - 2; i > 0 && history.length < 8; i--) {
      const move = Math.abs(closes[i + atrOffset]! - closes[i + atrOffset - 1]!)
      const r = move / atrArr[i]!
      if (r >= 1.0) history.push({ time: candles[i + atrOffset]!.time, ratio: +r.toFixed(2), direction: closes[i + atrOffset]! >= closes[i + atrOffset - 1]! ? 'bullish' : 'bearish', price: closes[i + atrOffset]! })
    }
    atrBreakout = { isBreakout: ratio >= 1.5, currentMove: +currentMove.toFixed(2), atr: +currentAtr.toFixed(2), ratio: +ratio.toFixed(2), direction, history }
  }

  // Divergences
  const allDivs: DivergenceResult[] = [
    ...detectDivergence(closes, times, rsiArr, 'RSI'),
    ...detectDivergence(closes, times, macdArr.map(m => m.histogram ?? 0), 'MACD'),
    ...detectDivergence(closes, times, cciArr as number[], 'CCI'),
    ...detectDivergence(closes, times, mfiArr as number[], 'MFI'),
  ]
  allDivs.sort((a, b) => a.barsAgo - b.barsAgo)

  // Markers
  type M = IndicatorMarker
  const mkr: Record<string, M[]> = {}
  const rsiOffset = closes.length - rsiArr.length
  const rsiMs: M[] = []
  for (let i = 1; i < rsiArr.length; i++) {
    const prev = rsiArr[i - 1]!, curr = rsiArr[i]!, t = times[i + rsiOffset]!
    if (prev < 70 && curr >= 70) rsiMs.push({ time: t, direction: 'bearish', label: 'RSI>70' })
    if (prev >= 70 && curr < 70) rsiMs.push({ time: t, direction: 'bullish', label: 'RSI↓70' })
    if (prev > 30 && curr <= 30) rsiMs.push({ time: t, direction: 'bearish', label: 'RSI<30' })
    if (prev <= 30 && curr > 30) rsiMs.push({ time: t, direction: 'bullish', label: 'RSI↑30' })
  }
  mkr['RSI'] = rsiMs
  const macdOffset = closes.length - macdArr.length
  const macdMs: M[] = []
  for (let i = 1; i < macdArr.length; i++) {
    const prevH = macdArr[i - 1]!.histogram ?? 0, currH = macdArr[i]!.histogram ?? 0, t = times[i + macdOffset]!
    if (prevH < 0 && currH >= 0) macdMs.push({ time: t, direction: 'bullish', label: 'MACD+' })
    if (prevH > 0 && currH <= 0) macdMs.push({ time: t, direction: 'bearish', label: 'MACD-' })
  }
  mkr['MACD'] = macdMs
  const stochOffset = closes.length - stochArr.length
  const stochMs: M[] = []
  for (let i = 1; i < stochArr.length; i++) {
    const prevK = stochArr[i - 1]!.k, currK = stochArr[i]!.k, t = times[i + stochOffset]!
    if (prevK < 80 && currK >= 80) stochMs.push({ time: t, direction: 'bearish', label: 'K>80' })
    if (prevK >= 80 && currK < 80) stochMs.push({ time: t, direction: 'bullish', label: 'K↓80' })
    if (prevK > 20 && currK <= 20) stochMs.push({ time: t, direction: 'bearish', label: 'K<20' })
    if (prevK <= 20 && currK > 20) stochMs.push({ time: t, direction: 'bullish', label: 'K↑20' })
  }
  mkr['Stoch'] = stochMs
  mkr['Supertrend'] = (supertrend?.crossovers ?? []).map(c => ({ time: c.time, direction: c.direction, label: c.direction === 'bullish' ? 'Buy' : 'Sell' }))
  mkr['Volume Spike'] = (volumeSpike?.history ?? []).map(e => ({ time: e.time, direction: e.direction, label: `×${e.ratio}` }))
  mkr['ATR Breakout'] = (atrBreakout?.history ?? []).map(e => ({ time: e.time, direction: e.direction, label: `×${e.ratio}` }))
  mkr['Divergence'] = allDivs.map(d => ({ time: d.time2, direction: d.type, label: d.indicator }))

  return { indicators, supertrend, volumeSpike, atrBreakout, divergences: { divergences: allDivs }, indicatorMarkers: mkr }
}

export function useMarketIndicators(symbol: string, interval: string) {
  const [connected, setConnected] = useState(false)
  const [loading, setLoading] = useState(true)
  const [indicators, setIndicators] = useState<IndicatorValue[]>([])
  const [supertrend, setSupertrend] = useState<SupertrendSignal | null>(null)
  const [volumeSpike, setVolumeSpike] = useState<VolumeSpikeSignal | null>(null)
  const [atrBreakout, setAtrBreakout] = useState<AtrBreakoutSignal | null>(null)
  const [divergences, setDivergences] = useState<MultiDivergenceSignal>({ divergences: [] })
  const [indicatorMarkers, setIndicatorMarkers] = useState<Record<string, IndicatorMarker[]>>({})

  const [candles, setCandles] = useState<Candle[]>([])
  const candlesRef = useRef<Candle[]>([])
  const fundingRateRef = useRef<number | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const fundingTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const lastCalcTimeRef = useRef(0)
  const symbolRef = useRef(symbol)
  const intervalRef = useRef(interval)

  function recalculate() {
    const now = Date.now()
    if (now - lastCalcTimeRef.current < CALC_THROTTLE_MS) return
    lastCalcTimeRef.current = now
    const result = calculateAll(candlesRef.current, fundingRateRef.current)
    if (!result) return
    setIndicators(result.indicators)
    setSupertrend(result.supertrend)
    setVolumeSpike(result.volumeSpike)
    setAtrBreakout(result.atrBreakout)
    setDivergences(result.divergences)
    setIndicatorMarkers(result.indicatorMarkers)
    setCandles([...candlesRef.current])
  }

  function disconnectWS() {
    if (reconnectTimerRef.current) { clearTimeout(reconnectTimerRef.current); reconnectTimerRef.current = null }
    if (wsRef.current) {
      const ws = wsRef.current
      ws.onopen = null; ws.onmessage = null; ws.onclose = null; ws.onerror = null
      ws.close()
      wsRef.current = null
    }
  }

  function connectWS(sym: string, iv: string) {
    disconnectWS()
    setConnected(false)
    const bybitIv = toBybitInterval(iv)
    const ws = new WebSocket('wss://stream.bybit.com/v5/public/linear')
    wsRef.current = ws

    ws.onopen = () => {
      if (wsRef.current !== ws) return
      ws.send(JSON.stringify({ op: 'subscribe', args: [`kline.${bybitIv}.${sym}`] }))
      setConnected(true)
    }

    ws.onmessage = (evt) => {
      if (wsRef.current !== ws) return
      try {
        const msg = JSON.parse(evt.data as string)
        if (!msg.topic?.startsWith('kline.') || !msg.data?.length) return
        const k = msg.data[0]
        const candle: Candle = {
          time: Number(k.start),
          open: parseFloat(k.open),
          high: parseFloat(k.high),
          low: parseFloat(k.low),
          close: parseFloat(k.close),
          volume: parseFloat(k.volume),
        }
        const cur = candlesRef.current
        if (k.confirm) {
          candlesRef.current = [...cur, candle].slice(-HISTORY_LIMIT)
        } else {
          const arr = [...cur]
          arr[arr.length - 1] = candle
          candlesRef.current = arr
        }
        recalculate()
      } catch { /* ignore */ }
    }

    ws.onclose = () => {
      if (wsRef.current !== ws) return
      setConnected(false)
      reconnectTimerRef.current = setTimeout(() => connectWS(symbolRef.current, intervalRef.current), 3000)
    }

    ws.onerror = () => ws.close()
  }

  async function fetchFunding(sym: string) {
    try {
      const res = await fetch(`https://api.bybit.com/v5/market/tickers?category=linear&symbol=${sym}`)
      const data = await res.json()
      const fr = data?.result?.list?.[0]?.fundingRate
      if (fr != null) fundingRateRef.current = parseFloat(fr) * 100
    } catch { /* ignore */ }
  }

  async function loadSymbol(sym: string, iv: string) {
    setLoading(true)
    candlesRef.current = []
    fundingRateRef.current = null

    const cacheKey = `${sym}-${iv}`
    const cached = candleCache[cacheKey]
    if (cached && Date.now() - cached.ts < CANDLE_TTL) {
      candlesRef.current = cached.candles
    } else {
      try {
        const bybitIv = toBybitInterval(iv)
        const res = await fetch(`https://api.bybit.com/v5/market/kline?category=linear&symbol=${sym}&interval=${bybitIv}&limit=${HISTORY_LIMIT}`)
        const data = await res.json()
        const list: string[][] = data?.result?.list ?? []
        const candles: Candle[] = list.reverse().map(k => ({
          time: Number(k[0]),
          open: parseFloat(k[1]!),
          high: parseFloat(k[2]!),
          low: parseFloat(k[3]!),
          close: parseFloat(k[4]!),
          volume: parseFloat(k[5]!),
        }))
        candlesRef.current = candles
        candleCache[cacheKey] = { candles, ts: Date.now() }
      } catch { /* ignore */ }
    }

    await fetchFunding(sym)
    const result = calculateAll(candlesRef.current, fundingRateRef.current)
    if (result) {
      setIndicators(result.indicators)
      setSupertrend(result.supertrend)
      setVolumeSpike(result.volumeSpike)
      setAtrBreakout(result.atrBreakout)
      setDivergences(result.divergences)
      setIndicatorMarkers(result.indicatorMarkers)
      setCandles([...candlesRef.current])
    }
    setLoading(false)
    connectWS(sym, iv)

    if (fundingTimerRef.current) clearInterval(fundingTimerRef.current)
    fundingTimerRef.current = setInterval(async () => {
      await fetchFunding(sym)
      recalculate()
    }, 30_000)
  }

  useEffect(() => {
    symbolRef.current = symbol
    intervalRef.current = interval
    loadSymbol(symbol, interval)
    return () => {
      disconnectWS()
      if (fundingTimerRef.current) { clearInterval(fundingTimerRef.current); fundingTimerRef.current = null }
    }
  }, [symbol, interval]) // eslint-disable-line react-hooks/exhaustive-deps

  return { indicators, connected, loading, supertrend, volumeSpike, atrBreakout, divergences, indicatorMarkers, candles }
}
