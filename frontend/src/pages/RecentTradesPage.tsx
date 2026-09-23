import { useState, useEffect, useRef, useCallback } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { getDashboardRecentTrades, type RecentTrade, type DashboardPeriod } from '../api/dashboard'
import { useSelectedAccount } from '../contexts/AccountContext'
import { T, grotesk, Card, NoData, useIsMobile } from '../components/dashboard/shared'
import { TradeRowHeader, TradeRow } from '../components/dashboard/TradeRow'

const PAGE_SIZE = 30

const PERIOD_LABELS: Record<DashboardPeriod, string> = {
  '1d': 'за 1 день', '7d': 'за 7 дней', '30d': 'за 30 дней',
  '90d': 'за 90 дней', '1y': 'за 1 год', 'all': 'за всё время',
}

function isDashboardPeriod(v: string | null): v is DashboardPeriod {
  return v === '1d' || v === '7d' || v === '30d' || v === '90d' || v === '1y' || v === 'all'
}

// Full-page expansion of the dashboard's "Последние сделки" widget: same account + period
// scope (via getDashboardRecentTrades, which shares its filtering logic with the widget's own
// GetDashboard query server-side), just without the widget's 10-row cap — loaded via infinite
// scroll instead of pagination controls.
export function RecentTradesPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const period: DashboardPeriod = isDashboardPeriod(searchParams.get('period')) ? searchParams.get('period') as DashboardPeriod : '30d'
  const { selectedAccountId } = useSelectedAccount()
  const isMobile = useIsMobile()

  const [trades, setTrades] = useState<RecentTrade[]>([])
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const sentinelRef = useRef<HTMLDivElement>(null)

  // Reset and load the first page whenever the scope (account or period) changes.
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    getDashboardRecentTrades(period, selectedAccountId || undefined, PAGE_SIZE, 0)
      .then(page => {
        if (cancelled) return
        setTrades(page.trades)
        setHasMore(page.has_more)
      })
      .catch(() => { if (!cancelled) setError('Не удалось загрузить сделки') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [period, selectedAccountId])

  const loadMore = useCallback(() => {
    setLoadingMore(prevLoading => {
      if (prevLoading) return prevLoading
      return true
    })
  }, [])

  // Actually perform the "load more" fetch once loadingMore flips to true (separate effect so
  // the IntersectionObserver callback below can stay a cheap, dependency-free flag flip).
  useEffect(() => {
    if (!loadingMore) return
    let cancelled = false
    getDashboardRecentTrades(period, selectedAccountId || undefined, PAGE_SIZE, trades.length)
      .then(page => {
        if (cancelled) return
        setTrades(prev => [...prev, ...page.trades])
        setHasMore(page.has_more)
      })
      .catch(() => { if (!cancelled) setError('Не удалось загрузить ещё сделки') })
      .finally(() => { if (!cancelled) setLoadingMore(false) })
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- trades.length is read once as the offset at trigger time, not a reactive dependency
  }, [loadingMore])

  useEffect(() => {
    if (!hasMore || loading) return
    const el = sentinelRef.current
    if (!el) return
    const observer = new IntersectionObserver(entries => {
      if (entries[0]?.isIntersecting) loadMore()
    }, { rootMargin: '200px' })
    observer.observe(el)
    return () => observer.disconnect()
  }, [hasMore, loading, loadMore])

  return (
    <div style={{ maxWidth: 900, margin: '0 auto', padding: isMobile ? '12px' : '24px' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 16 }}>
        <button
          type="button"
          onClick={() => navigate(-1)}
          style={{
            display: 'flex', alignItems: 'center', gap: 6, padding: '7px 12px', borderRadius: 9,
            background: 'rgba(255,255,255,.04)', border: `1px solid ${T.border}`,
            color: T.body, fontSize: 12, fontWeight: 600, cursor: 'pointer', flexShrink: 0,
          }}
        >
          <span aria-hidden>←</span> Назад
        </button>
        <div>
          <h1 style={{ margin: 0, ...grotesk, fontSize: 18, fontWeight: 700, color: T.text }}>Все сделки</h1>
          <div style={{ fontSize: 11, color: T.dim, marginTop: 2 }}>{PERIOD_LABELS[period]}</div>
        </div>
      </div>

      <Card pad="0" style={{ overflow: 'hidden' }}>
        {loading ? (
          <div style={{ textAlign: 'center', padding: '60px 0', color: T.dim }}>
            <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke={T.blue} strokeWidth={2} strokeLinecap="round" style={{ animation: 'spin 1s linear infinite' }}>
              <path d="M3 12a9 9 0 1 0 3-6.7" /><path d="M3 4v5h5" />
            </svg>
            <div style={{ marginTop: 12, fontSize: 14 }}>Загрузка данных…</div>
          </div>
        ) : error ? (
          <div style={{ padding: '20px', color: T.red, fontSize: 13, textAlign: 'center' }}>{error}</div>
        ) : trades.length === 0 ? (
          <NoData text="Нет закрытых сделок" />
        ) : (
          <>
            <TradeRowHeader isMobile={isMobile} />
            {trades.map((t, i) => (
              <TradeRow key={t.id} t={t} isMobile={isMobile} isLast={i === trades.length - 1 && !hasMore} />
            ))}
            {hasMore && (
              <div ref={sentinelRef} style={{ textAlign: 'center', padding: '16px 0', color: T.dim, fontSize: 12 }}>
                {loadingMore ? 'Загрузка…' : ' '}
              </div>
            )}
            {!hasMore && (
              <div style={{ textAlign: 'center', padding: '14px 0', color: T.faint, fontSize: 11 }}>
                Это все сделки за выбранный период
              </div>
            )}
          </>
        )}
      </Card>

      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </div>
  )
}
