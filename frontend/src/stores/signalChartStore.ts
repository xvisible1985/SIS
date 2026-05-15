export interface SignalChartIntent {
  signalId: string
  signalName: string
  params: Record<string, unknown>
  symbol: string
  tf: string
}

let _intent: SignalChartIntent | null = null

export function setSignalChartIntent(intent: SignalChartIntent): void {
  _intent = intent
}

/** Reads and clears the stored intent. Returns null if none was set. */
export function popSignalChartIntent(): SignalChartIntent | null {
  const v = _intent
  _intent = null
  return v
}
