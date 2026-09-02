import { describe, it, expect } from 'vitest'
import { BOT_KINDS, BOT_KIND_META, getBotKindMeta } from '../botKindMeta'
import type { BotKind } from '../types'

describe('botKindMeta', () => {
  it('BOT_KINDS содержит signal, parser, hedge, matrix, multi', () => {
    expect(BOT_KINDS).toEqual(['signal', 'parser', 'hedge', 'matrix', 'multi'])
  })

  it('signal имеет label SignalBot и не disabled', () => {
    expect(BOT_KIND_META['signal'].label).toBe('SignalBot')
    expect(BOT_KIND_META['signal'].disabled).toBeFalsy()
  })

  it('parser имеет disabled: true, hedge — доступен', () => {
    expect(BOT_KIND_META['parser'].disabled).toBe(true)
    expect(BOT_KIND_META['hedge'].disabled).toBeFalsy()
  })

  it('getBotKindMeta возвращает signal для неизвестного kind', () => {
    expect(getBotKindMeta('trend')).toBe(BOT_KIND_META['signal'])
    expect(getBotKindMeta(undefined)).toBe(BOT_KIND_META['signal'])
  })
})
