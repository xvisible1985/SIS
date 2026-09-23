import type { RecentTrade } from '../../api/dashboard'
import { T, mono, fmt$, fmtPct } from './shared'

// One row of the "Последние сделки" table, shared by the dashboard widget
// (DashboardPage.tsx's RecentTradesCard) and its expanded full-page view
// (RecentTradesPage.tsx) so the two stay pixel-identical.

export function tradeRowCols(isMobile: boolean): string {
  return isMobile ? 'minmax(0,1fr) 36px 76px' : '78px minmax(0,1.2fr) 40px minmax(0,1fr) 80px 58px'
}

export function TradeRowHeader({ isMobile }: { isMobile: boolean }) {
  return (
    <div style={{
      display: 'grid', gridTemplateColumns: tradeRowCols(isMobile),
      padding: '8px 12px', fontSize: 10, color: T.dim, textTransform: 'uppercase',
      letterSpacing: '1.2px', fontWeight: 600, borderBottom: `1px solid ${T.border}`,
      position: 'sticky', top: 0, background: T.panel, zIndex: 1,
    }}>
      {!isMobile && <div>Дата</div>}
      <div>Символ</div>
      <div></div>
      {!isMobile && <div>Бот</div>}
      <div style={{ textAlign: 'right' }}>P&L</div>
      {!isMobile && <div style={{ textAlign: 'right' }}>%</div>}
    </div>
  )
}

export function TradeRow({ t, isMobile, isLast }: { t: RecentTrade; isMobile: boolean; isLast: boolean }) {
  const isLong = t.direction === 'long'
  const isTP = t.result === 'tp'
  const d = new Date(t.closed_at)
  const ds = `${String(d.getDate()).padStart(2, '0')}.${String(d.getMonth() + 1).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  return (
    <div style={{
      display: 'grid', gridTemplateColumns: tradeRowCols(isMobile),
      padding: isMobile ? '9px 12px' : '9px 16px', alignItems: 'center', fontSize: 12,
      borderBottom: isLast ? 'none' : `1px solid ${T.border}`,
    }}>
      {!isMobile && <div style={{ ...mono, color: T.dim, fontSize: 11 }}>{ds}</div>}
      <div style={{ display: 'flex', alignItems: 'center', gap: 4, minWidth: 0 }}>
        <span style={{ ...mono, fontWeight: 700, color: T.text, fontSize: isMobile ? 11 : 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {isMobile ? t.symbol.replace('USDT', '') : t.symbol}
        </span>
        <span style={{
          padding: '1px 4px', borderRadius: 3, fontSize: 9, fontWeight: 700,
          background: isTP ? T.greenSoft : T.redSoft,
          border: `1px solid ${isTP ? T.greenBd : T.redBd}`,
          color: isTP ? T.green : T.red, textTransform: 'uppercase', flexShrink: 0,
        }}>{t.result.toUpperCase()}</span>
      </div>
      <div>
        <span style={{
          padding: '1px 5px', borderRadius: 3, fontSize: 9, fontWeight: 700,
          background: isLong ? T.greenSoft : T.redSoft,
          border: `1px solid ${isLong ? T.greenBd : T.redBd}`,
          color: isLong ? T.green : T.red, textTransform: 'uppercase',
        }}>{isLong ? 'L' : 'S'}</span>
      </div>
      {!isMobile && (
        <div style={{ fontSize: 11, color: T.body, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {t.bot_name ?? '—'}
        </div>
      )}
      <div style={{ textAlign: 'right' }}>
        <div style={{ ...mono, fontSize: isMobile ? 11 : 13, fontWeight: 700, color: (t.pnl ?? 0) >= 0 ? T.green : T.red }}>
          {t.pnl != null ? ((t.pnl >= 0 ? '+' : '') + fmt$(t.pnl)) : '—'}
        </div>
        {isMobile && t.pnl_pct != null && (
          <div style={{ ...mono, fontSize: 10, color: (t.pnl_pct ?? 0) >= 0 ? T.green : T.red }}>{fmtPct(t.pnl_pct)}</div>
        )}
      </div>
      {!isMobile && (
        <div style={{ textAlign: 'right', ...mono, fontSize: 11, color: (t.pnl_pct ?? 0) >= 0 ? T.green : T.red }}>
          {t.pnl_pct != null ? fmtPct(t.pnl_pct) : '—'}
        </div>
      )}
    </div>
  )
}
