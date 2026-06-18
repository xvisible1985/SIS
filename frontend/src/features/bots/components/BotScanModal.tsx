import { useState, useEffect, useRef, useCallback } from 'react';
import { Loader2, RefreshCw } from 'lucide-react';
import { apiClient } from '../../../api/client';
import type { Bot } from '../types';

// ── Regular scan types ──────────────────────────────────────────────────────

type ScanHit = {
  symbol: string;
  signal_state: 'buy' | 'sell';
  direction: 'long' | 'short';
  already_open: boolean;
  dir_blocked?: boolean;
  signal_value?: number;
  strength?: number;
  ttl_remaining_sec?: number; // -1 = no TTL; ≥0 = seconds left
};

type PreviewCfg = {
  strategy_type?: string;
  direction?: string;
  leverage?: number;
  margin_type?: string;
  hedge_mode?: boolean;
  grid_size_usdt?: number;
  grid_levels?: number;
  grid_active?: number;
  max_stop_active?: number;
  grid_step_pct?: number;
  tp_pct?: number;
  tp_mode?: string;
  sl_pct?: number;
  sl_type?: string;
  trailing_stop_enabled?: boolean;
  trailing_activation_pct?: number;
  trailing_callback_pct?: number;
};

type RegularScanResp = {
  mode?: undefined;
  results: ScanHit[];
  scanned: number;
  activation_signals: string[];
  preview: PreviewCfg;
};

// ── Hedge scan types ────────────────────────────────────────────────────────

type HedgeScanPos = {
  symbol: string;
  main_dir: 'long' | 'short';
  hedge_dir: 'long' | 'short';
  size: number;
  entry_price: number;
  mark_price: number;
  unrealised_pnl: number;
  metric_label: string;
  metric_value: number;
  threshold: number;
  meets_criteria: boolean;
  status: 'hedged' | 'ready' | 'monitoring';
  hedge_strat_id?: string;
  hedge_pnl?: number;
  hedge_size?: number;
};

type HedgeScanResp = {
  mode: 'hedge';
  positions: HedgeScanPos[];
};

type ScanResp = RegularScanResp | HedgeScanResp;

// ── Props ───────────────────────────────────────────────────────────────────

type Props = {
  bot: Bot;
  onClose: () => void;
};

// ── Component ───────────────────────────────────────────────────────────────

export function BotScanModal({ bot, onClose }: Props) {
  const [data, setData]       = useState<ScanResp | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError]     = useState<string | null>(null);

  // Regular scan state
  const [confirm, setConfirm]     = useState<ScanHit | null>(null);
  const [triggering, setTriggering] = useState(false);
  const [triggered, setTriggered]   = useState<Set<string>>(new Set());

  // Dragging
  const [pos, setPos] = useState({ left: Math.max(40, window.innerWidth / 2 - 260), top: 80 });
  const drag = useRef<{ mx: number; my: number; left: number; top: number } | null>(null);

  useEffect(() => {
    function onMove(e: MouseEvent) {
      if (!drag.current) return;
      setPos({ left: drag.current.left + e.clientX - drag.current.mx, top: drag.current.top + e.clientY - drag.current.my });
    }
    function onUp() { drag.current = null; }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => { window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp); };
  }, []);

  function onHeaderMouseDown(e: React.MouseEvent) {
    if ((e.target as HTMLElement).closest('button')) return;
    drag.current = { mx: e.clientX, my: e.clientY, left: pos.left, top: pos.top };
    e.preventDefault();
  }

  const scan = useCallback(async () => {
    setLoading(true);
    setError(null);
    setData(null);
    setConfirm(null);
    try {
      const res = await apiClient.get<ScanResp>(`/bots/${bot.id}/scan`);
      setData(res.data);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Ошибка сканирования');
    } finally {
      setLoading(false);
    }
  }, [bot.id]);

  // Авто-скан при открытии
  useEffect(() => { scan(); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const trigger = async (hit: ScanHit) => {
    setTriggering(true);
    try {
      await apiClient.post(`/bots/${bot.id}/trigger`, { symbol: hit.symbol, direction: hit.direction });
      setTriggered(prev => new Set([...prev, `${hit.symbol}:${hit.direction}`]));
      setConfirm(null);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Ошибка запуска');
    } finally {
      setTriggering(false);
    }
  };

  const isHedgeMode = data?.mode === 'hedge';
  const hedgeData   = isHedgeMode ? (data as HedgeScanResp) : null;
  const regularData = !isHedgeMode ? (data as RegularScanResp | null) : null;

  const activeCount = bot.activeStrategiesCount + triggered.size;
  const maxStrat    = bot.maxStrategies ?? 0;
  const limitReached = maxStrat > 0 && activeCount >= maxStrat;

  const actionableHits = regularData
    ? regularData.results.filter(h => !h.dir_blocked && !h.already_open && !triggered.has(`${h.symbol}:${h.direction}`))
    : [];
  const availableSlots = maxStrat > 0 ? Math.max(0, maxStrat - activeCount) : actionableHits.length;
  const priorityRank = new Map(
    actionableHits.slice(0, availableSlots).map((h, i) => [`${h.symbol}:${h.direction}`, i + 1])
  );

  const cfg = regularData?.preview;

  // Hedge counts
  const hedgedCount    = hedgeData?.positions.filter(p => p.status === 'hedged').length ?? 0;
  const readyCount     = hedgeData?.positions.filter(p => p.status === 'ready').length ?? 0;
  const monitoringCount = hedgeData?.positions.filter(p => p.status === 'monitoring').length ?? 0;

  const modalWidth = isHedgeMode ? 560 : 480;

  return (
    <div
      className="fixed z-[9999] flex flex-col rounded-xl overflow-hidden shadow-2xl"
      style={{ left: pos.left, top: pos.top, width: modalWidth, maxHeight: '80vh', background: '#1a1a2e', border: '1px solid #3a3a5c' }}
    >
      {/* Header */}
      <div
        className="flex items-center justify-between px-4 py-2 border-b cursor-move select-none shrink-0"
        style={{ background: '#1e1e32', borderColor: '#2e2e48' }}
        onMouseDown={onHeaderMouseDown}
      >
        <div className="flex items-center gap-3">
          <span className="text-[11px] font-mono font-semibold text-slate-200">
            {isHedgeMode ? '🛡 ХЕДЖ-МОНИТОР' : '⚡ ТЕСТ БОТА'} ·{' '}
            <span className="text-[#a78bfa]">{bot.name}</span>
          </span>
          {isHedgeMode && hedgeData && !loading && (
            <div className="flex items-center gap-1.5">
              {hedgedCount > 0 && (
                <span className="text-[10px] font-mono font-semibold text-emerald-400 bg-emerald-500/10 rounded px-1">
                  ✓ {hedgedCount}
                </span>
              )}
              {readyCount > 0 && (
                <span className="text-[10px] font-mono font-semibold text-amber-400 bg-amber-500/10 rounded px-1">
                  ⚡ {readyCount}
                </span>
              )}
              {monitoringCount > 0 && (
                <span className="text-[10px] font-mono text-slate-500 bg-white/[.04] rounded px-1">
                  👁 {monitoringCount}
                </span>
              )}
            </div>
          )}
          {!isHedgeMode && regularData && !loading && (
            <span className="text-[10px] text-slate-500">
              просканировано: <span className="text-slate-300">{regularData.scanned}</span>
            </span>
          )}
          {!isHedgeMode && maxStrat > 0 && (
            <span className={`text-[10px] font-mono font-semibold ${limitReached ? 'text-rose-400' : 'text-slate-400'}`}>
              {activeCount}/{maxStrat}
            </span>
          )}
          {loading && <Loader2 size={11} className="animate-spin text-slate-500" />}
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={scan}
            disabled={loading}
            className="flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-semibold text-slate-300 border border-white/[.10] bg-white/[.04] hover:bg-white/[.10] disabled:opacity-40 transition-colors"
            title="Обновить"
          >
            <RefreshCw size={9} className={loading ? 'animate-spin' : ''} />
            {isHedgeMode ? 'Обновить' : 'Скан'}
          </button>
          <button
            onClick={onClose}
            className="w-5 h-5 flex items-center justify-center text-slate-400 hover:text-slate-100 text-xs rounded"
          >✕</button>
        </div>
      </div>

      {/* Regular bot: activation signals bar */}
      {!isHedgeMode && regularData && regularData.activation_signals.length > 0 && (
        <div className="shrink-0 px-4 py-1.5 flex items-center gap-2 border-b" style={{ borderColor: '#2e2e48', background: 'rgba(91,58,237,.06)' }}>
          <span className="text-[10px] text-slate-500 uppercase tracking-wider font-semibold shrink-0">Сигналы</span>
          {regularData.activation_signals.map(s => (
            <span key={s} className="font-mono text-[10px] font-semibold text-[#a78bfa] bg-[#5b3aed]/[.15] border border-[#7c3aed]/30 rounded px-1.5 py-px">
              {s}
            </span>
          ))}
        </div>
      )}

      {/* Error bar */}
      {error && (
        <div className="shrink-0 px-4 py-1.5 text-[11px] text-rose-300 border-b" style={{ borderColor: '#2e2e48', background: 'rgba(248,113,113,.07)' }}>
          ⚠ {error}
        </div>
      )}

      {/* Body */}
      <div className="flex-1 overflow-auto">
        {!data && !loading && (
          <div className="px-4 py-8 text-center text-[12px] text-slate-500">
            {isHedgeMode ? 'Нажмите «Обновить»' : 'Нажмите «Скан» чтобы проверить рынок'}
          </div>
        )}
        {loading && (
          <div className="px-4 py-8 text-center text-[12px] text-slate-500">
            {isHedgeMode ? 'Загружаем позиции…' : 'Сканируем символы…'}
          </div>
        )}

        {/* ── Hedge mode view ── */}
        {isHedgeMode && hedgeData && !loading && (
          hedgeData.positions.length === 0 ? (
            <div className="px-4 py-8 text-center text-[12px] text-slate-500">
              Нет открытых позиций в зоне мониторинга
            </div>
          ) : (
            <table className="w-full text-[11px] font-mono">
              <thead>
                <tr className="text-[10px] text-slate-500 border-b" style={{ borderColor: '#272740' }}>
                  <th className="px-3 py-1.5 text-left font-normal">Символ</th>
                  <th className="px-3 py-1.5 text-left font-normal">Позиция</th>
                  <th className="px-3 py-1.5 text-right font-normal">PnL</th>
                  <th className="px-3 py-1.5 text-left font-normal">Метрика</th>
                  <th className="px-3 py-1.5 text-left font-normal">Статус</th>
                </tr>
              </thead>
              <tbody>
                {hedgeData.positions.map(p => (
                  <HedgePosRow key={`${p.symbol}:${p.main_dir}`} pos={p} />
                ))}
              </tbody>
            </table>
          )
        )}

        {/* ── Regular mode view ── */}
        {!isHedgeMode && regularData && !loading && (
          regularData.results.length === 0 ? (
            <div className="px-4 py-8 text-center text-[12px] text-slate-500">
              Нет символов с активными сигналами
            </div>
          ) : (
            <>
            {priorityRank.size > 0 && (
              <div className="shrink-0 px-3 py-1.5 flex items-center gap-1.5 border-b text-[10px]" style={{ borderColor: '#1e1e30', background: 'rgba(251,191,36,.04)' }}>
                <span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 2, background: 'rgba(251,191,36,.55)', flexShrink: 0 }} />
                <span className="text-amber-400/80 font-semibold">
                  {limitReached
                    ? 'Лимит достигнут — новые стратегии не откроются'
                    : `Приоритет на запуск: ${priorityRank.size} пар${maxStrat > 0 ? ` (слотов: ${availableSlots})` : ''}`
                  }
                </span>
              </div>
            )}
            <table className="w-full text-[11px] font-mono">
              <thead>
                <tr className="text-[10px] text-slate-500 border-b" style={{ borderColor: '#272740' }}>
                  <th className="px-3 py-1.5 text-left font-normal">Символ</th>
                  <th className="px-3 py-1.5 text-left font-normal">Сигнал</th>
                  <th className="px-3 py-1.5 text-left font-normal">Направление</th>
                  <th className="px-3 py-1.5 text-left font-normal">TTL</th>
                  <th className="px-3 py-1.5 text-left font-normal">Статус</th>
                  <th className="px-3 py-1.5 text-right font-normal">Действие</th>
                </tr>
              </thead>
              <tbody>
                {regularData.results.map(hit => {
                  const key = `${hit.symbol}:${hit.direction}`;
                  const isDone = triggered.has(key);
                  const isConfirming = confirm?.symbol === hit.symbol && confirm?.direction === hit.direction;
                  const isBlocked = !!hit.dir_blocked;
                  const rank = priorityRank.get(key);
                  const isPriority = rank !== undefined;
                  return (
                    <tr
                      key={key}
                      className="border-b hover:bg-white/[.03] transition-colors"
                      style={{
                        borderColor: '#1e1e30',
                        background: isConfirming
                          ? 'rgba(91,58,237,.08)'
                          : isPriority
                            ? 'rgba(251,191,36,.03)'
                            : undefined,
                        boxShadow: isPriority ? 'inset 3px 0 0 rgba(251,191,36,.55)' : undefined,
                        opacity: isBlocked ? 0.45 : 1,
                      }}
                    >
                      <td className="px-3 py-2 text-slate-100 font-semibold">
                        <div className="flex items-center gap-1.5">
                          {rank != null && (
                            <span className="inline-flex h-[15px] min-w-[15px] items-center justify-center rounded-sm bg-amber-500/20 px-1 text-[9px] font-bold tabular-nums text-amber-400">
                              {rank}
                            </span>
                          )}
                          <span>
                            {hit.symbol.replace(/USDT$/i, '')}
                            <span className="text-slate-600">USDT</span>
                          </span>
                        </div>
                      </td>
                      <td className="px-3 py-2">
                        <span className={hit.signal_state === 'buy' ? 'text-emerald-400' : 'text-rose-400'}>
                          {hit.signal_state === 'buy' ? '▲ BUY' : '▼ SELL'}
                          {hit.signal_value != null && hit.signal_value > 0 && (
                            <span className="ml-1 opacity-50 text-[9px]">{hit.signal_value.toFixed(1)}</span>
                          )}
                        </span>
                      </td>
                      <td className="px-3 py-2">
                        <span className={hit.direction === 'long' ? 'text-blue-400' : 'text-orange-400'}>
                          {hit.direction.toUpperCase()}
                        </span>
                      </td>
                      <td className="px-3 py-2">
                        {(() => {
                          const ttl = fmtTTL(hit.ttl_remaining_sec);
                          if (!ttl) return <span className="text-slate-700">—</span>;
                          const sec = hit.ttl_remaining_sec ?? -1;
                          const urgent = sec >= 0 && sec < 120;
                          return (
                            <span className={urgent ? 'text-amber-400' : 'text-slate-400'}>
                              {ttl}
                            </span>
                          );
                        })()}
                      </td>
                      <td className="px-3 py-2">
                        {isBlocked
                          ? <span className="text-slate-600 text-[10px]">не то направление</span>
                          : isDone
                            ? <span className="text-emerald-400">✓ запущено</span>
                            : hit.already_open
                              ? <span className="text-slate-500">открыта</span>
                              : isConfirming
                                ? <span className="text-[#a78bfa]">подтверди →</span>
                                : <span className="text-slate-600">—</span>
                        }
                      </td>
                      <td className="px-3 py-2 text-right">
                        {!isBlocked && !isDone && !hit.already_open && (
                          isConfirming ? (
                            <button
                              type="button"
                              onClick={() => setConfirm(null)}
                              className="text-[10px] text-slate-500 hover:text-slate-300 transition-colors"
                            >
                              отмена
                            </button>
                          ) : limitReached ? (
                            <span className="text-[10px] text-slate-600">лимит</span>
                          ) : (
                            <button
                              type="button"
                              onClick={() => setConfirm(hit)}
                              className="text-[10px] font-semibold text-[#a78bfa] hover:text-[#c4b5fd] transition-colors"
                            >
                              Запустить
                            </button>
                          )
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            </>
          )
        )}
      </div>

      {/* Regular mode: Confirmation panel */}
      {!isHedgeMode && confirm && cfg && (
        <div className="shrink-0 border-t" style={{ borderColor: '#3a3a5c', background: '#202035' }}>
          <div className="px-4 py-2 border-b text-[10px] font-semibold text-slate-400 uppercase tracking-wider" style={{ borderColor: '#2e2e48' }}>
            Подтверждение · {confirm.symbol} {confirm.direction.toUpperCase()}
          </div>
          <div className="px-4 py-2.5">
            <div className="mb-2.5 text-[11px] text-slate-400">
              <span className="text-slate-500">Почему: </span>
              сигнал{' '}
              <span className={confirm.signal_state === 'buy' ? 'text-emerald-400 font-semibold' : 'text-rose-400 font-semibold'}>
                {confirm.signal_state.toUpperCase()}
              </span>
              {regularData && regularData.activation_signals.length > 0 && (
                <span className="text-slate-500"> ({regularData.activation_signals.join(', ')})</span>
              )}
              {' → '}
              <span className={confirm.direction === 'long' ? 'text-blue-400 font-semibold' : 'text-orange-400 font-semibold'}>
                {confirm.direction.toUpperCase()}
              </span>
            </div>
            <div className="grid grid-cols-3 gap-x-4 gap-y-1 mb-3 font-mono text-[10px]">
              <KV k="тип" v={cfg.strategy_type ?? 'grid'} />
              <KV k="плечо" v={`×${cfg.leverage ?? 1}`} />
              <KV k="маржа" v={cfg.margin_type ?? 'isolated'} />
              <KV k="лот USDT" v={String(cfg.grid_size_usdt ?? 100)} />
              <KV k="уровней" v={String(cfg.grid_levels ?? '—')} />
              <KV k="шаг %" v={String(cfg.grid_step_pct ?? '—')} />
              <KV k="TP %" v={`${cfg.tp_pct ?? '—'} (${cfg.tp_mode ?? '—'})`} />
              <KV k="SL %" v={`${cfg.sl_pct ?? '—'} (${cfg.sl_type ?? '—'})`} />
              {(cfg.grid_active ?? 0) > 0 && (
                <KV k="акт. ордеров" v={String(cfg.grid_active)} />
              )}
              {(cfg.max_stop_active ?? 0) > 0 && (
                <KV k="усл. стопов" v={String(cfg.max_stop_active)} />
              )}
              {cfg.trailing_stop_enabled && (
                <KV k="трейлинг" v={`${cfg.trailing_activation_pct}% / ${cfg.trailing_callback_pct}%`} />
              )}
            </div>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => setConfirm(null)}
                className="flex-1 py-1.5 rounded text-[11px] font-semibold text-slate-400 border border-white/[.08] bg-white/[.03] hover:bg-white/[.07] transition-colors"
              >
                Отмена
              </button>
              <button
                type="button"
                onClick={() => trigger(confirm)}
                disabled={triggering || limitReached}
                className="flex-1 py-1.5 rounded text-[11px] font-semibold text-white border border-[#7c3aed]/40 disabled:opacity-50 transition-colors"
                style={{ background: triggering ? '#3a2a8a' : 'linear-gradient(180deg,#5b3aed,#4a2dcc)' }}
              >
                {triggering ? 'Запускаем…' : 'Подтвердить'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// ── Hedge position row ──────────────────────────────────────────────────────

function HedgePosRow({ pos }: { pos: HedgeScanPos }) {
  const isHedged    = pos.status === 'hedged';
  const isReady     = pos.status === 'ready';

  // Row accent color based on status
  const rowStyle: React.CSSProperties = {
    borderColor: '#1e1e30',
    background: isHedged
      ? 'rgba(52,211,153,.03)'
      : isReady
        ? 'rgba(251,191,36,.03)'
        : undefined,
    boxShadow: isHedged
      ? 'inset 3px 0 0 rgba(52,211,153,.5)'
      : isReady
        ? 'inset 3px 0 0 rgba(251,191,36,.5)'
        : undefined,
  };

  // Metric progress bar
  const pct = pos.threshold > 0 ? Math.min(100, (pos.metric_value / pos.threshold) * 100) : 0;
  const barColor = isHedged ? '#34d399' : isReady ? '#fbbf24' : '#6366f1';

  const totalPnl = pos.unrealised_pnl + (isHedged ? (pos.hedge_pnl ?? 0) : 0);

  return (
    <tr className="border-b hover:bg-white/[.02] transition-colors" style={rowStyle}>
      {/* Symbol + dirs */}
      <td className="px-3 py-2 text-slate-100 font-semibold">
        <div>
          <span>{pos.symbol.replace(/USDT$/i, '')}<span className="text-slate-600">USDT</span></span>
        </div>
        <div className="flex items-center gap-1 mt-0.5">
          <span className={`text-[9px] font-semibold ${pos.main_dir === 'long' ? 'text-blue-400' : 'text-orange-400'}`}>
            {pos.main_dir === 'long' ? '▲' : '▼'} {pos.main_dir.toUpperCase()}
          </span>
          {isHedged && (
            <>
              <span className="text-slate-600 text-[9px]">→</span>
              <span className={`text-[9px] font-semibold ${pos.hedge_dir === 'long' ? 'text-blue-400' : 'text-orange-400'}`}>
                {pos.hedge_dir === 'long' ? '▲' : '▼'} хедж
              </span>
            </>
          )}
        </div>
      </td>

      {/* Position info */}
      <td className="px-3 py-2">
        <div className="text-slate-300 text-[10px]">{pos.size} · ×</div>
        <div className="text-slate-500 text-[9px]">
          вход {fmtPrice(pos.entry_price)}
          {isHedged && pos.hedge_size ? (
            <span className="ml-1 text-slate-600">/ хедж {pos.hedge_size}</span>
          ) : null}
        </div>
      </td>

      {/* PnL */}
      <td className="px-3 py-2 text-right">
        <div className={`text-[11px] font-semibold ${pos.unrealised_pnl >= 0 ? 'text-emerald-400' : 'text-rose-400'}`}>
          {pos.unrealised_pnl >= 0 ? '+' : ''}{pos.unrealised_pnl.toFixed(2)}$
        </div>
        {isHedged && (
          <div className={`text-[9px] ${totalPnl >= 0 ? 'text-emerald-500' : 'text-rose-500'} opacity-70`}>
            итого {totalPnl >= 0 ? '+' : ''}{totalPnl.toFixed(2)}$
          </div>
        )}
      </td>

      {/* Metric */}
      <td className="px-3 py-2" style={{ minWidth: 110 }}>
        <div className="text-[9px] text-slate-500 mb-0.5">{pos.metric_label}</div>
        <div className="flex items-center gap-1.5">
          <span className={`text-[10px] font-semibold tabular-nums ${pos.meets_criteria ? 'text-amber-300' : 'text-slate-300'}`}>
            {pos.metric_value.toFixed(2)}
          </span>
          <span className="text-slate-600 text-[9px]">/ {pos.threshold.toFixed(2)}</span>
        </div>
        {/* Progress bar */}
        <div className="mt-1 h-[3px] rounded-full bg-white/[.06] overflow-hidden" style={{ width: 90 }}>
          <div
            className="h-full rounded-full transition-all"
            style={{ width: `${pct}%`, background: barColor, opacity: 0.7 }}
          />
        </div>
      </td>

      {/* Status badge */}
      <td className="px-3 py-2">
        {isHedged ? (
          <span className="inline-flex items-center gap-1 text-[9px] font-semibold text-emerald-400 bg-emerald-500/10 border border-emerald-500/20 rounded px-1.5 py-0.5">
            ✓ Хеджируется
          </span>
        ) : isReady ? (
          <span className="inline-flex items-center gap-1 text-[9px] font-semibold text-amber-400 bg-amber-500/10 border border-amber-500/20 rounded px-1.5 py-0.5">
            ⚡ Условие
          </span>
        ) : (
          <span className="inline-flex items-center gap-1 text-[9px] text-slate-500 bg-white/[.03] border border-white/[.06] rounded px-1.5 py-0.5">
            👁 Мониторинг
          </span>
        )}
      </td>
    </tr>
  );
}

// ── Helpers ─────────────────────────────────────────────────────────────────

function fmtTTL(sec: number | undefined): string | null {
  if (sec === undefined || sec < 0) return null;
  if (sec === 0) return '0с';
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return m > 0 ? `${m}м ${s}с` : `${s}с`;
}

function fmtPrice(price: number): string {
  if (price >= 1000) return price.toFixed(1);
  if (price >= 1)    return price.toFixed(3);
  return price.toFixed(6);
}

function KV({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex items-baseline gap-1">
      <span className="text-slate-500">{k}</span>
      <span className="text-slate-200 font-semibold">{v}</span>
    </div>
  );
}
