import type { IndicatorCategory } from './types';

export const CAT_STYLES: Record<IndicatorCategory, { mark: string; tag: string; label: string }> = {
  momentum: {
    mark:  'bg-[linear-gradient(135deg,rgba(91,140,255,.25),rgba(123,91,255,.18))] text-[#b8c8ff] border-[#5b8cff]/40',
    tag:   'bg-[linear-gradient(135deg,rgba(91,140,255,.25),rgba(123,91,255,.18))] text-[#b8c8ff] border-[#5b8cff]/40',
    label: 'Momentum',
  },
  trend: {
    mark:  'bg-[linear-gradient(135deg,rgba(65,210,139,.22),rgba(65,210,139,.10))] text-emerald-300 border-emerald-400/35',
    tag:   'bg-[linear-gradient(135deg,rgba(65,210,139,.22),rgba(65,210,139,.10))] text-emerald-300 border-emerald-400/35',
    label: 'Trend',
  },
  volatility: {
    mark:  'bg-[linear-gradient(135deg,rgba(247,166,0,.22),rgba(247,166,0,.10))] text-amber-400 border-amber-500/35',
    tag:   'bg-[linear-gradient(135deg,rgba(247,166,0,.22),rgba(247,166,0,.10))] text-amber-400 border-amber-500/35',
    label: 'Volatility',
  },
  volume: {
    mark:  'bg-[linear-gradient(135deg,rgba(193,77,255,.22),rgba(193,77,255,.10))] text-[#d8a4ff] border-[#c14dff]/40',
    tag:   'bg-[linear-gradient(135deg,rgba(193,77,255,.22),rgba(193,77,255,.10))] text-[#d8a4ff] border-[#c14dff]/40',
    label: 'Volume',
  },
};

export const SOURCES = ['close', 'open', 'high', 'low', 'hl2', 'ohlc4'] as const;
export const TFS     = ['1m', '5m', '15m', '1h', '4h', '1D'] as const;
