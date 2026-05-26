import {
  Grid3x3, Layers, Signal, Zap, RefreshCw, Share2, TrendingUp, Shield,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import type { BotStrategy, RiskLevel } from './ui-types';

/** Иконка + текст + цвета для каждой стратегии */
export const STRAT_META: Record<BotStrategy, {
  label: string;
  icon: LucideIcon;
  /** background gradient (css) */
  bg: string;
  /** primary text/icon color */
  color: string;
  /** border colour */
  border: string;
}> = {
  grid:      { label: 'Grid',      icon: Grid3x3,    bg: 'linear-gradient(135deg, rgba(91,140,255,.25), rgba(123,91,255,.18))',  color: '#b8c8ff', border: 'rgba(91,140,255,.4)' },
  matrix:    { label: 'Matrix',    icon: Layers,     bg: 'linear-gradient(135deg, rgba(65,210,139,.22), rgba(65,210,139,.10))',  color: '#5be0a0', border: 'rgba(65,210,139,.35)' },
  signal:    { label: 'Signal',    icon: Signal,     bg: 'linear-gradient(135deg, rgba(247,166,0,.22), rgba(247,166,0,.10))',    color: '#f7a600', border: 'rgba(247,166,0,.35)' },
  scalp:     { label: 'Scalper',   icon: Zap,        bg: 'linear-gradient(135deg, rgba(193,77,255,.22), rgba(193,77,255,.10))',  color: '#d8a4ff', border: 'rgba(193,77,255,.4)' },
  arbitrage: { label: 'Arbitrage', icon: RefreshCw,  bg: 'linear-gradient(135deg, rgba(91,224,160,.20), rgba(91,140,255,.14))',  color: '#7eecb4', border: 'rgba(91,224,160,.4)' },
  copy:      { label: 'Copy',      icon: Share2,     bg: 'linear-gradient(135deg, rgba(255,123,87,.22), rgba(255,123,87,.10))',  color: '#ff9b78', border: 'rgba(255,123,87,.4)' },
  trend:     { label: 'Trend',     icon: TrendingUp, bg: 'linear-gradient(135deg, rgba(91,140,255,.22), rgba(65,210,139,.14))',  color: '#9ec0ff', border: 'rgba(91,140,255,.4)' },
  hold:      { label: 'HODL',      icon: Shield,     bg: 'linear-gradient(135deg, rgba(255,255,255,.10), rgba(255,255,255,.04))', color: '#cfd5e1', border: 'rgba(255,255,255,.15)' },
};

export const RISK_META: Record<RiskLevel, { label: string; color: string; bg: string; border: string }> = {
  low:    { label: 'Низкий риск',  color: '#5be0a0', bg: 'rgba(65,210,139,.14)',  border: 'rgba(65,210,139,.28)' },
  medium: { label: 'Средний риск', color: '#f7a600', bg: 'rgba(247,166,0,.14)',   border: 'rgba(247,166,0,.28)' },
  high:   { label: 'Высокий риск', color: '#fca5a5', bg: 'rgba(248,113,113,.14)', border: 'rgba(248,113,113,.28)' },
};
