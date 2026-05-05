import type { LogEntry } from '../../hooks/terminal/usePositionsWs'

interface Props { log: LogEntry[] }

export function TradeLog({ log }: Props) {
  if (!log.length) return (
    <div className="flex flex-col items-center justify-center h-full text-gray-500 dark:text-gray-400 gap-2">
      <span className="text-2xl opacity-30">💻</span>
      <p className="text-sm">Ожидание событий...</p>
    </div>
  )
  return (
    <table className="w-full text-xs">
      <thead className="sticky top-0 z-10 bg-white dark:bg-gray-900">
        <tr className="text-gray-500 dark:text-gray-400 border-b border-gray-200 dark:border-gray-700">
          <th className="text-left px-3 py-2 w-20">Время</th>
          <th className="text-left px-3 py-2">Сообщение</th>
        </tr>
      </thead>
      <tbody>
        {log.map((row, i) => (
          <tr key={i} className={`border-b border-gray-200 dark:border-gray-700/30 ${row.error ? 'bg-red-500/5' : ''}`}>
            <td className="px-3 py-1.5 font-mono text-gray-500 dark:text-gray-400 whitespace-nowrap">{row.time}</td>
            <td className={`px-3 py-1.5 ${row.error ? 'text-red-400' : 'text-gray-700 dark:text-gray-200'}`}>{row.message}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
