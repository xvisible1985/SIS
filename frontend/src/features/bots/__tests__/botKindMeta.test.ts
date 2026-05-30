import { describe, it, expect } from 'vitest'
import { BOT_KINDS, BOT_KIND_META, getBotKindMeta } from '../botKindMeta'
import type { BotKind } from '../types'

describe('botKindMeta', () => {
  it('BOT_KINDS содержит signal, parser, hedge', () => {
    expect(BOT_KINDS).toEqual(['signal', 'parser', 'hedge'])
  })

  it('signal имеет label SignalBot и не disabled', () => {
    expect(BOT_KIND_META['signal'].label).toBe('SignalBot')
    expect(BOT_KIND_META['signal'].disabled).toBeFalsy()
  })

  it('parser и hedge имеют disabled: true', () => {
    expect(BOT_KIND_META['parser'].disabled).toBe(true)
    expect(BOT_KIND_META['hedge'].disabled).toBe(true)
  })

  it('getBotKindMeta возвращает signal для неизвестного kind', () => {
    expect(getBotKindMeta('trend' as BotKind)).toBe(BOT_KIND_META['signal'])
    expect(getBotKindMeta(undefined)).toBe(BOT_KIND_META['signal'])
  })
})
