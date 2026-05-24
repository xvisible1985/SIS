export type IndicatorCategory = 'momentum' | 'trend' | 'volatility' | 'volume' | 'fundamental';

export type SignalState = 'buy' | 'sell' | 'neutral';

export type IndicatorSignal = {
  state: SignalState;
  value?: string;
};

export type PriceSource = 'close' | 'open' | 'high' | 'low' | 'hl2' | 'ohlc4';
export type Timeframe = '1m' | '5m' | '15m' | '1h' | '4h' | '1D';

export type BaseParams = {
  source?: PriceSource;
  tf: Timeframe;
};

export type IndicatorParam =
  | { kind: 'number';    key: string; label: string; hint?: string; step?: number; min?: number; max?: number; suffix?: string; decimals?: number }
  | { kind: 'segmented'; key: string; label: string; hint?: string; options: string[] };

export type IndicatorDef<P extends BaseParams = BaseParams> = {
  id: string;
  abbr: string;
  name: string;
  cat: IndicatorCategory;
  desc: string;
  about?: string;
  defaults: P;
  params: IndicatorParam[];
  compute?: (params: P, candles: Candle[]) => IndicatorSignal;
  Preview?: React.FC<{ params: P; candles?: Candle[] }>;
};

export type SignalDef<P extends Record<string, unknown> = Record<string, unknown>> = {
  id: string;
  abbr: string;
  name: string;
  cat: IndicatorCategory;
  desc: string;
  about?: string;
  defaults: P;
  params: IndicatorParam[];
  formula: (params: P) => React.ReactNode;
  state?: SignalState;
  compute?: (params: P, candles: Candle[]) => SignalState;
};

export type Candle = {
  time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
};
