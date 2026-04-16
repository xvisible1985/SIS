interface ProgressBarProps {
  pct: number
  status: string
}

export function ProgressBar({ pct, status }: ProgressBarProps) {
  if (!status) return null
  return (
    <div className="space-y-1">
      <div className="flex justify-between text-xs text-gray-600">
        <span className="capitalize">{status}</span>
        <span>{pct}%</span>
      </div>
      <div className="h-2 bg-gray-200 rounded overflow-hidden">
        <div
          className="h-full bg-blue-500 transition-all duration-300"
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  )
}
