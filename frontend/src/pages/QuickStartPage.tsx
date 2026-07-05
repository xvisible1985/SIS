import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  CheckCircle2,
  AlertCircle,
  Loader2,
  Key,
  TrendingUp,
  TrendingDown,
  ArrowRight,
  Zap,
  ChevronRight,
  Wallet,
  Sparkles,
  Bot,
  Shield,
  Target,
} from 'lucide-react'
import { listAccounts, getAccountPositions, getAccountBalance } from '../api/accounts'
import { listBotPresets } from '../api/botPresets'
import type { BalanceResult } from '../api/accounts'
import type { BotPreset } from '../api/botPresets'
import type { ExchangeAccount, Position } from '../types'

type StepStatus = 'pending' | 'loading' | 'ok' | 'warn' | 'error'

interface StepState {
  apiKey: StepStatus
  recommendations: StepStatus
}

interface WizardAnswers {
  depositPct:     number | null
  aggressiveness: 'conservative' | 'moderate' | 'aggressive' | null
  coinType:       'stable' | 'meme' | 'both' | null
  tradingStyle:   'diversified' | 'concentrated' | null
}

// ── UI helpers ───────────────────────────────────────────────────────────────

function StepIndicator({ index, status, label }: { index: number; status: StepStatus; label: string }) {
  const active = status !== 'pending'
  return (
    <div className="flex items-center gap-3">
      <div
        className={`relative flex h-8 w-8 shrink-0 items-center justify-center rounded-full border text-[13px] font-bold transition-all duration-300 ${
          status === 'ok'      ? 'border-emerald-500/50 bg-emerald-500/15 text-emerald-400'
        : status === 'warn'   ? 'border-amber-500/50 bg-amber-500/15 text-amber-400'
        : status === 'loading'? 'border-[#4a7dff]/50 bg-[#4a7dff]/15 text-[#7b9fff]'
        : status === 'error'  ? 'border-rose-500/40 bg-rose-500/10 text-rose-400'
        :                       'border-white/10 bg-white/[.04] text-slate-500'
        }`}
      >
        {status === 'loading' ? <Loader2 size={14} className="animate-spin" />
        : status === 'ok'     ? <CheckCircle2 size={14} />
        : status === 'warn'   ? <AlertCircle size={14} />
        : <span>{index}</span>}
      </div>
      <span className={`text-[13px] font-medium transition-colors ${active ? 'text-slate-200' : 'text-slate-500'}`}>
        {label}
      </span>
    </div>
  )
}

function Card({ children, glow }: { children: React.ReactNode; glow?: 'blue' | 'green' | 'amber' | 'violet' }) {
  const glowClass =
    glow === 'blue'   ? 'border-[#4a7dff]/20 shadow-[0_8px_32px_-12px_rgba(74,125,255,.25)]'
  : glow === 'green'  ? 'border-emerald-500/20 shadow-[0_8px_32px_-12px_rgba(52,211,153,.2)]'
  : glow === 'amber'  ? 'border-amber-500/20 shadow-[0_8px_32px_-12px_rgba(251,191,36,.2)]'
  : glow === 'violet' ? 'border-violet-500/20 shadow-[0_8px_32px_-12px_rgba(139,92,246,.2)]'
  : 'border-white/[.08]'
  return (
    <div className={`relative overflow-hidden rounded-2xl border bg-[#111827] p-6 ${glowClass}`}>
      {children}
    </div>
  )
}

function OptionBtn({
  label, sublabel, selected, onClick,
}: { label: string; sublabel?: string; selected: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex flex-col items-start rounded-xl border px-4 py-3 text-left transition-all ${
        selected
          ? 'border-[#4a7dff]/50 bg-[#4a7dff]/15 shadow-[0_0_0_1px_rgba(74,125,255,.2)]'
          : 'border-white/[.07] bg-white/[.025] hover:border-white/20 hover:bg-white/[.04]'
      }`}
    >
      <span className={`text-[13px] font-semibold ${selected ? 'text-[#7b9fff]' : 'text-slate-200'}`}>{label}</span>
      {sublabel && <span className="mt-0.5 text-[11px] text-slate-500">{sublabel}</span>}
    </button>
  )
}

function PosCard({ pos }: { pos: Position }) {
  const pnl    = parseFloat(pos.unrealisedPnl)
  const pnlPct = parseFloat(pos.unrealisedPnlPct)
  const positive = pnl >= 0
  const isLong   = pos.side === 'Buy'
  return (
    <div className="flex items-center justify-between gap-4 rounded-xl border border-white/[.06] bg-white/[.025] px-4 py-3">
      <div className="flex items-center gap-3 min-w-0">
        <div className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${isLong ? 'bg-emerald-500/15 text-emerald-400' : 'bg-rose-500/15 text-rose-400'}`}>
          {isLong ? <TrendingUp size={15} /> : <TrendingDown size={15} />}
        </div>
        <div className="min-w-0">
          <div className="text-[13px] font-semibold text-slate-100 truncate">{pos.symbol}</div>
          <div className="text-[11px] text-slate-500 capitalize flex items-center gap-1.5">
            {isLong ? 'Long' : 'Short'} · {pos.leverage}x · {parseFloat(pos.size).toFixed(4)}
            {pos.positionIdx !== 0 && (
              <span className="rounded-[3px] border border-violet-500/30 bg-violet-500/[.12] px-1 py-px text-[9px] font-bold text-violet-400 normal-case">hedge</span>
            )}
          </div>
        </div>
      </div>
      <div className="text-right shrink-0">
        <div className={`text-[13px] font-semibold ${positive ? 'text-emerald-400' : 'text-rose-400'}`}>
          {positive ? '+' : ''}${pnl.toFixed(2)}
        </div>
        <div className={`text-[11px] font-medium ${positive ? 'text-emerald-500/70' : 'text-rose-500/70'}`}>
          {positive ? '+' : ''}{pnlPct.toFixed(2)}%
        </div>
      </div>
    </div>
  )
}

const AGG_COLORS: Record<string, string> = {
  conservative: 'border-emerald-500/30 bg-emerald-500/[.1] text-emerald-400',
  moderate:     'border-amber-500/30 bg-amber-500/[.1] text-amber-400',
  aggressive:   'border-rose-500/30 bg-rose-500/[.1] text-rose-400',
}
const AGG_LABELS: Record<string, string> = {
  conservative: 'Консервативный',
  moderate:     'Умеренный',
  aggressive:   'Агрессивный',
}

function PresetResultCard({ preset, equity, depositPct, onDeploy }: {
  preset: BotPreset
  equity: number | undefined
  depositPct: number | null
  onDeploy: () => void
}) {
  const allocatedUsd = (equity && depositPct) ? ((equity * depositPct) / 100).toFixed(0) : null
  const tradingBots  = preset.bots.filter(b => b.role === 'trading')
  const hedgingBots  = preset.bots.filter(b => b.role === 'hedging')

  return (
    <div className="rounded-xl border border-white/[.08] bg-white/[.025] p-4">
      <div className="flex items-start justify-between gap-3 mb-3">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap mb-0.5">
            <span className="text-[14px] font-semibold text-slate-100">{preset.name}</span>
            <span className={`rounded-[4px] border px-1.5 py-px text-[10px] font-bold ${AGG_COLORS[preset.aggressiveness]}`}>
              {AGG_LABELS[preset.aggressiveness]}
            </span>
            {preset.tags.map(t => (
              <span key={t} className="rounded-full border border-violet-500/25 bg-violet-500/[.08] px-2 py-px text-[10px] text-violet-400">#{t}</span>
            ))}
          </div>
          {preset.description && (
            <p className="text-[12px] text-slate-500">{preset.description}</p>
          )}
        </div>
        {allocatedUsd && (
          <div className="text-right shrink-0">
            <div className="text-[11px] text-slate-500">Выделяется</div>
            <div className="text-[14px] font-bold text-white">${allocatedUsd}</div>
          </div>
        )}
      </div>

      <div className="flex flex-col gap-1 mb-3">
        {tradingBots.map(b => (
          <div key={b.entry_id} className="flex items-center gap-2 text-[12px]">
            <Bot size={12} className="text-emerald-500 shrink-0" />
            <span className="text-slate-300">{b.name}</span>
            <span className="text-[10px] text-emerald-500/70">торговый</span>
          </div>
        ))}
        {hedgingBots.map(b => (
          <div key={b.entry_id} className="flex items-center gap-2 text-[12px]">
            <Shield size={12} className="text-violet-400 shrink-0" />
            <span className="text-slate-300">{b.name}</span>
            <span className="text-[10px] text-violet-400/70">хеджирующий</span>
          </div>
        ))}
        {preset.bots.length === 0 && (
          <span className="text-[12px] text-slate-600">Боты ещё не добавлены</span>
        )}
      </div>

      <button
        onClick={onDeploy}
        className="inline-flex items-center gap-1.5 rounded-lg border border-[#4a7dff]/35 bg-[#4a7dff]/[.12] px-3 py-2 text-[12px] font-semibold text-[#7b9fff] hover:bg-[#4a7dff]/20 transition-colors"
      >
        Запустить пресет <ArrowRight size={13} />
      </button>
    </div>
  )
}

// ── Main page ────────────────────────────────────────────────────────────────

export function QuickStartPage() {
  const navigate = useNavigate()

  // Step 1
  const [step, setStep]       = useState<StepState>({ apiKey: 'loading', recommendations: 'pending' })
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [positions, setPositions] = useState<Position[]>([])
  const [posStatus, setPosStatus] = useState<StepStatus>('pending')
  const [posError, setPosError]   = useState<string | null>(null)
  const [balance, setBalance]     = useState<BalanceResult | null>(null)
  const [balStatus, setBalStatus] = useState<StepStatus>('pending')
  const [balError, setBalError]   = useState<string | null>(null)

  // Step 2 wizard
  const [answers, setAnswers] = useState<WizardAnswers>({
    depositPct: null, aggressiveness: null, coinType: null, tradingStyle: null,
  })
  const [presets, setPresets]           = useState<BotPreset[]>([])
  const [presetsStatus, setPresetsStatus] = useState<StepStatus>('pending')
  const [wizardDone, setWizardDone]     = useState(false)

  // Load API key + accounts
  useEffect(() => {
    listAccounts()
      .then(accs => {
        setAccounts(accs)
        if (accs.length === 0) {
          setStep({ apiKey: 'warn', recommendations: 'pending' })
        } else {
          setStep(s => ({ ...s, apiKey: 'ok' }))
          setBalStatus('loading')
          setPosStatus('loading')
          const active = accs.find(a => a.is_active) ?? accs[0]
          Promise.allSettled([
            getAccountBalance(active.id),
            getAccountPositions(active.id),
          ]).then(([balRes, posRes]) => {
            if (balRes.status === 'fulfilled') {
              setBalance(balRes.value)
              setBalStatus(balRes.value.ok ? 'ok' : 'error')
              if (!balRes.value.ok) setBalError(balRes.value.message ?? 'Ошибка получения баланса')
            } else {
              setBalStatus('error')
              setBalError('Ошибка получения баланса')
            }
            if (posRes.status === 'fulfilled') {
              if (!posRes.value.ok) {
                setPosError(posRes.value.message ?? 'Ошибка получения позиций')
                setPosStatus('error')
              } else {
                setPositions(posRes.value.positions ?? [])
                setPosStatus('ok')
              }
            } else {
              setPosStatus('error')
              setPosError('Ошибка получения позиций')
            }
          })
        }
      })
      .catch(() => setStep({ apiKey: 'error', recommendations: 'pending' }))
  }, [])

  // Load presets when all 3 filtering answers are filled
  useEffect(() => {
    const { aggressiveness, coinType, tradingStyle } = answers
    if (!aggressiveness || !coinType || !tradingStyle) return
    setPresetsStatus('loading')
    listBotPresets({
      aggressiveness,
      coin_type: coinType,
      trading_style: tradingStyle,
    })
      .then(data => {
        setPresets(data)
        setPresetsStatus(data.length > 0 ? 'ok' : 'warn')
        setWizardDone(true)
        setStep(s => ({ ...s, recommendations: data.length > 0 ? 'ok' : 'warn' }))
      })
      .catch(() => {
        setPresetsStatus('error')
        setStep(s => ({ ...s, recommendations: 'error' }))
      })
  }, [answers.aggressiveness, answers.coinType, answers.tradingStyle])

  function setAnswer<K extends keyof WizardAnswers>(key: K, value: WizardAnswers[K]) {
    setAnswers(prev => ({ ...prev, [key]: value }))
  }

  const totalPnl     = positions.reduce((s, p) => s + parseFloat(p.unrealisedPnl), 0)
  const longCount    = positions.filter(p => p.side === 'Buy').length
  const shortCount   = positions.filter(p => p.side === 'Sell').length
  const drawdownPos  = positions.filter(p => parseFloat(p.unrealisedPnl) < 0)
  const hasDrawdown  = drawdownPos.length > 0
  const noPositions  = posStatus === 'ok' && positions.length === 0
  const step2Visible = step.apiKey === 'ok' && (posStatus === 'ok' || posStatus === 'error')

  // sidebar step 2 status: show as active (number) when step1 done but wizard incomplete
  const sidebar2Status: StepStatus =
    step.recommendations !== 'pending' ? step.recommendations
    : step2Visible ? 'pending'
    : 'pending'

  return (
    <div className="min-h-full bg-[#080d14] px-6 py-8">
      <div className="mx-auto max-w-2xl">

        {/* header */}
        <div className="mb-8">
          <div className="mb-3 inline-flex items-center gap-2 rounded-full border border-[#4a7dff]/25 bg-[#4a7dff]/10 px-3 py-1">
            <Zap size={12} className="text-[#7b9fff]" />
            <span className="text-[11px] font-semibold uppercase tracking-[1.2px] text-[#7b9fff]">Быстрый старт</span>
          </div>
          <h1 className="text-2xl font-bold tracking-tight text-white">Быстрый старт</h1>
          <p className="mt-1.5 text-[13px] text-slate-400">
            Подключите API ключ и получите персональные рекомендации
          </p>
        </div>

        <div className="flex gap-6">

          {/* Sidebar */}
          <div className="flex w-48 shrink-0 flex-col gap-4 pt-1">
            <StepIndicator index={1} status={step.apiKey} label="API ключ" />
            <div className="ml-[15px] h-5 w-px bg-white/[.06]" />
            <StepIndicator index={2} status={sidebar2Status} label="Рекомендации" />
          </div>

          {/* Content */}
          <div className="flex-1 flex flex-col gap-4">

            {/* ── Шаг 1: API ключ ── */}
            {step.apiKey === 'loading' && (
              <Card glow="blue">
                <div className="flex items-center gap-3 text-slate-400">
                  <Loader2 size={18} className="animate-spin text-[#4a7dff]" />
                  <span className="text-[13px]">Проверяем API ключи…</span>
                </div>
              </Card>
            )}

            {step.apiKey === 'warn' && (
              <Card glow="amber">
                <div className="pointer-events-none absolute -right-8 -top-8 h-32 w-32 rounded-full bg-[radial-gradient(circle,rgba(251,191,36,.12),transparent_65%)]" />
                <div className="mb-4 flex items-center gap-3">
                  <div className="flex h-10 w-10 items-center justify-center rounded-xl border border-amber-500/25 bg-amber-500/10">
                    <Key size={18} className="text-amber-400" />
                  </div>
                  <div>
                    <div className="text-[14px] font-semibold text-white">API ключи не найдены</div>
                    <div className="text-[12px] text-slate-400">Добавьте ключ для работы с биржей</div>
                  </div>
                </div>
                <p className="mb-4 text-[13px] leading-relaxed text-slate-400">
                  Для запуска ботов и отслеживания позиций необходимо подключить API ключ вашего биржевого аккаунта.
                </p>
                <button onClick={() => navigate('/accounts')}
                  className="inline-flex items-center gap-2 rounded-lg bg-amber-500/15 border border-amber-500/25 px-4 py-2.5 text-[13px] font-semibold text-amber-300 hover:bg-amber-500/25 transition-colors">
                  Добавить API ключ <ArrowRight size={14} />
                </button>
              </Card>
            )}

            {step.apiKey === 'error' && (
              <Card>
                <div className="flex items-center gap-3 text-rose-400">
                  <AlertCircle size={18} />
                  <span className="text-[13px]">Не удалось загрузить аккаунты. Попробуйте обновить страницу.</span>
                </div>
              </Card>
            )}

            {step.apiKey === 'ok' && (
              <Card glow="green">
                <div className="pointer-events-none absolute -right-8 -top-8 h-32 w-32 rounded-full bg-[radial-gradient(circle,rgba(52,211,153,.1),transparent_65%)]" />
                <div className="flex items-center gap-3">
                  <div className="flex h-10 w-10 items-center justify-center rounded-xl border border-emerald-500/25 bg-emerald-500/10">
                    <Key size={18} className="text-emerald-400" />
                  </div>
                  <div className="flex-1">
                    <div className="text-[14px] font-semibold text-white">API ключ подключён</div>
                    <div className="text-[12px] text-slate-400">
                      {accounts.filter(a => a.is_active).length} активных / {accounts.length} аккаунтов
                    </div>
                  </div>
                  <button onClick={() => navigate('/accounts')}
                    className="flex items-center gap-1 text-[12px] text-slate-500 hover:text-slate-300 transition-colors">
                    Управление <ChevronRight size={12} />
                  </button>
                </div>
              </Card>
            )}

            {/* Баланс и позиции */}
            {step.apiKey === 'ok' && (
              <Card glow="green">
                <div className="pointer-events-none absolute -right-8 -top-8 h-32 w-32 rounded-full bg-[radial-gradient(circle,rgba(52,211,153,.08),transparent_65%)]" />

                {/* Баланс */}
                <div className="mb-5">
                  <div className="flex items-center gap-2 mb-3">
                    <Wallet size={14} className="text-slate-400" />
                    <span className="text-[12px] font-semibold uppercase tracking-[1px] text-slate-400">Торговый депозит</span>
                  </div>
                  {balStatus === 'loading' && (
                    <div className="flex items-center gap-2 text-slate-500 text-[13px]">
                      <Loader2 size={13} className="animate-spin" /><span>Загружаем баланс…</span>
                    </div>
                  )}
                  {balStatus === 'error' && (
                    <div className="flex items-center gap-2 text-rose-400 text-[13px]">
                      <AlertCircle size={13} /><span>{balError ?? 'Ошибка загрузки баланса'}</span>
                    </div>
                  )}
                  {balStatus === 'ok' && balance && (
                    <div className="flex gap-3">
                      <div className="flex-1 rounded-xl border border-white/[.06] bg-white/[.025] px-4 py-3">
                        <div className="text-[11px] font-semibold uppercase tracking-[1px] text-slate-500 mb-1">Эквити</div>
                        <div className="text-[18px] font-bold text-white">${balance.equity != null ? balance.equity.toFixed(2) : '—'}</div>
                        {balance.equity_change_usd != null && (
                          <div className={`text-[11px] font-medium mt-0.5 ${balance.equity_change_usd >= 0 ? 'text-emerald-500/70' : 'text-rose-500/70'}`}>
                            {balance.equity_change_usd >= 0 ? '+' : ''}${balance.equity_change_usd.toFixed(2)} за 24ч
                          </div>
                        )}
                      </div>
                      <div className="flex-1 rounded-xl border border-white/[.06] bg-white/[.025] px-4 py-3">
                        <div className="text-[11px] font-semibold uppercase tracking-[1px] text-slate-500 mb-1">Доступно</div>
                        <div className="text-[18px] font-bold text-white">${balance.available != null ? balance.available.toFixed(2) : '—'}</div>
                        <div className="text-[11px] text-slate-600 mt-0.5">свободная маржа</div>
                      </div>
                      {balance.equity_change_percent != null && (
                        <div className="flex-none rounded-xl border border-white/[.06] bg-white/[.025] px-4 py-3 text-center">
                          <div className="text-[11px] font-semibold uppercase tracking-[1px] text-slate-500 mb-1">24ч</div>
                          <div className={`text-[18px] font-bold ${balance.equity_change_percent >= 0 ? 'text-emerald-400' : 'text-rose-400'}`}>
                            {balance.equity_change_percent >= 0 ? '+' : ''}{balance.equity_change_percent.toFixed(2)}%
                          </div>
                        </div>
                      )}
                    </div>
                  )}
                </div>

                <div className="border-t border-white/[.06] mb-5" />

                {/* Позиции */}
                <div>
                  <div className="flex items-center gap-2 mb-3">
                    <TrendingUp size={14} className="text-slate-400" />
                    <span className="text-[12px] font-semibold uppercase tracking-[1px] text-slate-400">Открытые позиции</span>
                  </div>
                  {posStatus === 'loading' && (
                    <div className="flex items-center gap-2 text-slate-500 text-[13px]">
                      <Loader2 size={13} className="animate-spin" /><span>Загружаем позиции…</span>
                    </div>
                  )}
                  {posStatus === 'error' && (
                    <div className="flex items-center gap-2 text-rose-400 text-[13px]">
                      <AlertCircle size={13} /><span>{posError ?? 'Ошибка загрузки позиций'}</span>
                    </div>
                  )}
                  {posStatus === 'ok' && positions.length === 0 && (
                    <p className="text-[13px] text-slate-500">Активных позиций на бирже не найдено</p>
                  )}
                  {posStatus === 'ok' && positions.length > 0 && (
                    <>
                      <div className="mb-3 flex items-center justify-between">
                        <span className="text-[12px] text-slate-400">
                          {positions.length} позиций · {longCount} long · {shortCount} short
                        </span>
                        <span className={`text-[13px] font-bold ${totalPnl >= 0 ? 'text-emerald-400' : 'text-rose-400'}`}>
                          {totalPnl >= 0 ? '+' : ''}${totalPnl.toFixed(2)}
                        </span>
                      </div>
                      <div className="flex flex-col gap-2">
                        {positions.map((pos, i) => (
                          <PosCard key={`${pos.symbol}-${pos.positionIdx}-${i}`} pos={pos} />
                        ))}
                      </div>
                    </>
                  )}
                </div>

                <div className="mt-5 pt-4 border-t border-white/[.06]">
                  <button onClick={() => navigate('/terminal')}
                    className="inline-flex items-center gap-2 rounded-lg bg-[#4a7dff]/15 border border-[#4a7dff]/25 px-4 py-2.5 text-[13px] font-semibold text-[#7b9fff] hover:bg-[#4a7dff]/25 transition-colors">
                    Открыть терминал <ArrowRight size={14} />
                  </button>
                </div>
              </Card>
            )}

            {/* ── Шаг 2: Просадка ── */}
            {step2Visible && hasDrawdown && (
              <Card glow="amber">
                <div className="pointer-events-none absolute -right-8 -top-8 h-32 w-32 rounded-full bg-[radial-gradient(circle,rgba(251,191,36,.1),transparent_65%)]" />
                <div className="mb-4 flex items-center gap-3">
                  <div className="flex h-10 w-10 items-center justify-center rounded-xl border border-amber-500/25 bg-amber-500/10">
                    <Target size={18} className="text-amber-400" />
                  </div>
                  <div>
                    <div className="text-[14px] font-semibold text-white">Обнаружены позиции в просадке</div>
                    <div className="text-[12px] text-slate-400">
                      {drawdownPos.length} поз. · суммарно{' '}
                      <span className="text-rose-400 font-semibold">
                        ${drawdownPos.reduce((s, p) => s + parseFloat(p.unrealisedPnl), 0).toFixed(2)}
                      </span>
                    </div>
                  </div>
                </div>
                <div className="flex flex-col gap-2 mb-4">
                  {drawdownPos.map((pos, i) => (
                    <PosCard key={`dd-${pos.symbol}-${i}`} pos={pos} />
                  ))}
                </div>
                <div className="rounded-xl border border-amber-500/15 bg-amber-500/[.06] px-4 py-3 text-[13px] text-amber-300/80">
                  Стратегии для вывода из просадки появятся в ближайших обновлениях.
                  Следите за разделом «Пресеты» в каталоге ботов.
                </div>
              </Card>
            )}

            {/* ── Шаг 2: Опросник ── */}
            {step2Visible && noPositions && (
              <Card glow="violet">
                <div className="pointer-events-none absolute -right-8 -top-8 h-32 w-32 rounded-full bg-[radial-gradient(circle,rgba(139,92,246,.1),transparent_65%)]" />

                <div className="mb-5 flex items-center gap-3">
                  <div className="flex h-10 w-10 items-center justify-center rounded-xl border border-violet-500/25 bg-violet-500/10">
                    <Sparkles size={18} className="text-violet-400" />
                  </div>
                  <div>
                    <div className="text-[14px] font-semibold text-white">Подберём оптимальный пресет</div>
                    <div className="text-[12px] text-slate-400">Ответьте на несколько вопросов</div>
                  </div>
                </div>

                <div className="flex flex-col gap-5">

                  {/* Q1 — доля депозита */}
                  <div>
                    <div className="mb-2 text-[12px] font-semibold text-slate-300">
                      Какую часть депозита задействуем?
                    </div>
                    <div className="grid grid-cols-4 gap-2">
                      {([25, 50, 75, 100] as const).map(pct => (
                        <OptionBtn
                          key={pct}
                          label={`${pct}%`}
                          sublabel={balance?.equity != null ? `$${((balance.equity * pct) / 100).toFixed(0)}` : undefined}
                          selected={answers.depositPct === pct}
                          onClick={() => setAnswer('depositPct', pct)}
                        />
                      ))}
                    </div>
                  </div>

                  {/* Q2 — агрессивность (появляется после Q1) */}
                  {answers.depositPct !== null && (
                    <div>
                      <div className="mb-2 text-[12px] font-semibold text-slate-300">
                        Насколько агрессивно торгуем?
                      </div>
                      <div className="grid grid-cols-3 gap-2">
                        <OptionBtn label="Консервативно" sublabel="Меньше риска" selected={answers.aggressiveness === 'conservative'} onClick={() => setAnswer('aggressiveness', 'conservative')} />
                        <OptionBtn label="Умеренно"      sublabel="Баланс"        selected={answers.aggressiveness === 'moderate'}     onClick={() => setAnswer('aggressiveness', 'moderate')} />
                        <OptionBtn label="Агрессивно"    sublabel="Больше риска"  selected={answers.aggressiveness === 'aggressive'}   onClick={() => setAnswer('aggressiveness', 'aggressive')} />
                      </div>
                    </div>
                  )}

                  {/* Q3 — монеты (после Q2) */}
                  {answers.aggressiveness !== null && (
                    <div>
                      <div className="mb-2 text-[12px] font-semibold text-slate-300">
                        Какие монеты предпочитаете?
                      </div>
                      <div className="grid grid-cols-3 gap-2">
                        <OptionBtn label="Стабильные" sublabel="BTC, ETH, SOL"   selected={answers.coinType === 'stable'} onClick={() => setAnswer('coinType', 'stable')} />
                        <OptionBtn label="Мемы"       sublabel="DOGE, PEPE, ..."  selected={answers.coinType === 'meme'}   onClick={() => setAnswer('coinType', 'meme')} />
                        <OptionBtn label="Не важно"   sublabel="Любые"            selected={answers.coinType === 'both'}   onClick={() => setAnswer('coinType', 'both')} />
                      </div>
                    </div>
                  )}

                  {/* Q4 — стиль (после Q3) */}
                  {answers.coinType !== null && (
                    <div>
                      <div className="mb-2 text-[12px] font-semibold text-slate-300">
                        Стиль распределения позиций?
                      </div>
                      <div className="grid grid-cols-2 gap-2">
                        <OptionBtn label="Много монет"     sublabel="Мелкие лоты, диверсификация" selected={answers.tradingStyle === 'diversified'}  onClick={() => setAnswer('tradingStyle', 'diversified')} />
                        <OptionBtn label="1-2 монеты"      sublabel="Крупные лоты, концентрация"  selected={answers.tradingStyle === 'concentrated'} onClick={() => setAnswer('tradingStyle', 'concentrated')} />
                      </div>
                    </div>
                  )}

                  {/* Результаты */}
                  {answers.tradingStyle !== null && (
                    <div className="border-t border-white/[.06] pt-4">
                      {presetsStatus === 'loading' && (
                        <div className="flex items-center gap-2 text-slate-500 text-[13px]">
                          <Loader2 size={14} className="animate-spin" />
                          <span>Подбираем пресеты…</span>
                        </div>
                      )}

                      {presetsStatus === 'warn' && (
                        <div className="rounded-xl border border-amber-500/20 bg-amber-500/[.06] px-4 py-3 text-[13px] text-amber-300/80">
                          По вашим параметрам пресетов пока нет. Попробуйте изменить ответы выше или{' '}
                          <button onClick={() => navigate('/bots')} className="underline hover:text-amber-200">просмотрите каталог ботов</button>.
                        </div>
                      )}

                      {presetsStatus === 'ok' && presets.length > 0 && (
                        <div className="flex flex-col gap-3">
                          <div className="flex items-center gap-2 mb-1">
                            <CheckCircle2 size={14} className="text-emerald-400" />
                            <span className="text-[13px] font-semibold text-slate-200">
                              Найдено {presets.length} подходящих {presets.length === 1 ? 'пресет' : 'пресета'}
                            </span>
                          </div>
                          {presets.map(preset => (
                            <PresetResultCard
                              key={preset.id}
                              preset={preset}
                              equity={balance?.equity}
                              depositPct={answers.depositPct}
                              onDeploy={() => navigate('/bots')}
                            />
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              </Card>
            )}

            {/* Шаг 2: позиции есть, просадок нет — просто показываем CTA */}
            {step2Visible && !noPositions && !hasDrawdown && posStatus === 'ok' && (
              <Card glow="blue">
                <div className="flex items-center gap-3">
                  <div className="flex h-10 w-10 items-center justify-center rounded-xl border border-[#4a7dff]/25 bg-[#4a7dff]/10">
                    <CheckCircle2 size={18} className="text-[#7b9fff]" />
                  </div>
                  <div className="flex-1">
                    <div className="text-[14px] font-semibold text-white">Портфель в хорошем состоянии</div>
                    <div className="text-[12px] text-slate-400">Все позиции в плюсе</div>
                  </div>
                  <button onClick={() => navigate('/terminal')}
                    className="flex items-center gap-1 text-[12px] text-slate-500 hover:text-slate-300 transition-colors">
                    Терминал <ChevronRight size={12} />
                  </button>
                </div>
              </Card>
            )}

          </div>
        </div>
      </div>
    </div>
  )
}
