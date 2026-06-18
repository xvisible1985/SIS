import type { BotKind } from './types'

export interface BotKindMeta {
  id:         BotKind
  label:      string
  tagline:    string
  desc:       string
  color:      string
  border:     string
  bg:         string
  bgHeader:   string
  iconBg:     string
  disabled?:  boolean
}

export const BOT_KINDS: BotKind[] = ['signal', 'parser', 'hedge', 'matrix']

export const BOT_KIND_META: Record<BotKind, BotKindMeta> = {
  signal: {
    id:       'signal',
    label:    'SignalBot',
    tagline:  'Следует за рыночным трендом',
    desc:     'Открывает позиции в направлении доминирующего тренда по сигналам индикаторов. Использует сетку ордеров для усреднения входа и автоматически переворачивает позицию при смене тренда.',
    color:    '#60a5fa',
    border:   'rgba(59,130,246,0.35)',
    bg:       'rgba(59,130,246,0.10)',
    bgHeader: 'linear-gradient(135deg,rgba(59,130,246,0.20) 0%,rgba(37,99,235,0.06) 100%)',
    iconBg:   'rgba(59,130,246,0.18)',
  },
  parser: {
    id:       'parser',
    label:    'ParserBot',
    tagline:  'Торгует на новостях и листингах',
    desc:     'Парсит анонсы бирж, новости и листинги в реальном времени. При появлении триггера мгновенно открывает позицию до основной реакции рынка, фиксируя быструю волатильность.',
    color:    '#22d3ee',
    border:   'rgba(6,182,212,0.35)',
    bg:       'rgba(6,182,212,0.10)',
    bgHeader: 'linear-gradient(135deg,rgba(6,182,212,0.20) 0%,rgba(8,145,178,0.06) 100%)',
    iconBg:   'rgba(6,182,212,0.18)',
    disabled: true,
  },
  hedge: {
    id:       'hedge',
    label:    'HedgeBot',
    tagline:  'Хеджирует риски портфеля',
    desc:     'Удерживает контр-позиции для защиты от просадок. Автоматически балансирует лонг и шорт на основе текущей рыночной экспозиции и заданного коэффициента хеджирования.',
    color:    '#f59e0b',
    border:   'rgba(180,83,9,0.45)',
    bg:       'rgba(180,83,9,0.15)',
    bgHeader: 'linear-gradient(135deg,rgba(180,83,9,0.24) 0%,rgba(120,53,15,0.06) 100%)',
    iconBg:   'rgba(180,83,9,0.20)',
  },
  matrix: {
    id:       'matrix',
    label:    'MatrixBot',
    tagline:  'Парная матрица с симметричным закрытием',
    desc:     'Автоматически открывает зеркальные матричные позиции лонг и шорт на каждом символе. Закрывает пару когда суммарный PnL достигает цели — без привязки к внешним позициям.',
    color:    '#a78bfa',
    border:   'rgba(124,58,237,0.40)',
    bg:       'rgba(124,58,237,0.12)',
    bgHeader: 'linear-gradient(135deg,rgba(124,58,237,0.22) 0%,rgba(91,33,182,0.06) 100%)',
    iconBg:   'rgba(124,58,237,0.20)',
  },
}

export function getBotKindMeta(kind: BotKind | string | undefined): BotKindMeta {
  return BOT_KIND_META[kind as BotKind] ?? BOT_KIND_META['signal']
}
