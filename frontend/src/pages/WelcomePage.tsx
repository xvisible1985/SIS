import { useState, useEffect, type CSSProperties } from 'react'
import { useNavigate } from 'react-router-dom'
import { listAccounts } from '../api/accounts'

// ─── Tokens ───────────────────────────────────────────────────────────────────
const T = {
  bg:        '#080b12',
  panel:     '#0d1119',
  border:    'rgba(255,255,255,.07)',
  borderHi:  'rgba(255,255,255,.14)',
  text:      '#f2f5fb',
  body:      '#dde3ef',
  dim:       '#7b8aa6',
  blue:      '#5b8cff',
  blueSoft:  'rgba(91,140,255,.12)',
  blueBd:    'rgba(91,140,255,.38)',
  green:     '#5be0a0',
  greenSoft: 'rgba(65,210,139,.13)',
  greenBd:   'rgba(65,210,139,.28)',
  orange:    '#f7a600',
  purple:    '#c14dff',
}
const grotesk: CSSProperties = { fontFamily: "'Space Grotesk', sans-serif" }

// ─── Types ────────────────────────────────────────────────────────────────────
interface Answers {
  experience: string[]      // multi-select
  status:     string | null
  deposit:    string | null
  strategy:   string | null
}
const EMPTY: Answers = { experience: [], status: null, deposit: null, strategy: null }
const LS_KEY = 'sis_onboarding_v2'
const DONE_KEY = 'sis_onboarding_done'
const markDone = () => localStorage.setItem(DONE_KEY, '1')

function load(): Answers {
  try {
    const raw = JSON.parse(localStorage.getItem(LS_KEY) ?? 'null')
    if (!raw) return EMPTY
    // migrate old format where experience was a single string
    const exp = Array.isArray(raw.experience)
      ? raw.experience
      : raw.experience ? [raw.experience] : []
    return { experience: exp, status: raw.status ?? null, deposit: raw.deposit ?? null, strategy: raw.strategy ?? null }
  } catch { return EMPTY }
}
function save(a: Answers) { localStorage.setItem(LS_KEY, JSON.stringify(a)) }

function isAnswered(answers: Answers, key: keyof Answers): boolean {
  const v = answers[key]
  return Array.isArray(v) ? v.length > 0 : v !== null
}

// ─── Step definitions ─────────────────────────────────────────────────────────
interface StepOpt { value: string; icon: string; label: string; sub: string }
interface Step { key: keyof Answers; title: string; sub: string; opts: StepOpt[]; multi?: boolean }

const STEPS: Step[] = [
  {
    key: 'experience', multi: true,
    title: 'Ваш опыт в трейдинге',
    sub: 'Можно выбрать несколько вариантов',
    opts: [
      { value: 'beginner',     icon: '🌱', label: 'Новичок',       sub: 'Менее 1 года'  },
      { value: 'intermediate', icon: '📈', label: 'Средний',        sub: '1–3 года'      },
      { value: 'experienced',  icon: '🎯', label: 'Опытный',        sub: '3–5 лет'       },
      { value: 'pro',          icon: '🏆', label: 'Профессионал',   sub: '5+ лет'        },
    ],
  },
  {
    key: 'status',
    title: 'Ваш статус сейчас',
    sub: 'Есть ли у вас активность на бирже прямо сейчас?',
    opts: [
      { value: 'none',      icon: '🔍', label: 'Только изучаю',    sub: 'Ещё не начал торговать' },
      { value: 'manual',    icon: '✋', label: 'Торгую вручную',   sub: 'Сам слежу за позициями' },
      { value: 'bots',      icon: '🤖', label: 'Использую ботов',  sub: 'Уже есть автоматизация' },
      { value: 'positions', icon: '💼', label: 'Есть позиции',     sub: 'Открытые прямо сейчас'  },
    ],
  },
  {
    key: 'deposit',
    title: 'Планируемый депозит',
    sub: 'Примерно, сколько планируете выделить на торговлю',
    opts: [
      { value: 'micro',  icon: '🌱', label: 'До $500',          sub: 'Для начала и теста' },
      { value: 'small',  icon: '💰', label: '$500 – $2 000',    sub: 'Стартовый капитал'  },
      { value: 'medium', icon: '💎', label: '$2 000 – $10 000', sub: 'Активная торговля'  },
      { value: 'large',  icon: '🚀', label: 'Более $10 000',    sub: 'Серьёзный трейдинг' },
    ],
  },
  {
    key: 'strategy',
    title: 'Интересные стратегии',
    sub: 'Выберите то, что кажется наиболее подходящим',
    opts: [
      { value: 'grid',   icon: '⬜', label: 'Grid',         sub: 'Сетка покупок/продаж в диапазоне' },
      { value: 'matrix', icon: '⬡', label: 'Matrix',       sub: 'Сложные многоуровневые сетки'     },
      { value: 'trend',  icon: '🌊', label: 'Тренд',        sub: 'Следование движению рынка'        },
      { value: 'unsure', icon: '🤔', label: 'Пока не знаю', sub: 'Помогите с выбором'               },
    ],
  },
]

// ─── Recommendations ──────────────────────────────────────────────────────────
interface Rec { icon: string; title: string; body: string; accent: string }

function buildRecs(a: Answers): Rec[] {
  const exp = a.experience
  const isPro = exp.includes('pro')
  const isExp = exp.includes('experienced')
  const preferMatrix = a.strategy === 'matrix' || isPro
    || (isExp && a.strategy !== 'grid' && a.strategy !== 'unsure')

  const r1: Rec = preferMatrix
    ? { icon: '⬡', title: 'Matrix стратегия — для опытных',
        body: 'Многоуровневые сетки с динамическим управлением позицией. Больше гибкости и контроля над рисками.',
        accent: T.purple }
    : { icon: '⬜', title: 'Grid стратегия — отличный старт',
        body: 'Автоматически покупает дешевле и продаёт дороже в заданном диапазоне. Идеально для нестабильных рынков.',
        accent: T.blue }

  const r2: Rec = a.deposit === 'micro'
    ? { icon: '⚖️', title: 'Торгуй без плеча',
        body: 'При депозите до $500 рекомендуем работать без плеча или 2×. Так изучишь систему без серьёзного риска.',
        accent: T.orange }
    : a.deposit === 'large'
    ? { icon: '🎯', title: 'Диверсифицируй капитал',
        body: 'Распредели депозит между 4–6 ботами на разных инструментах для снижения максимальной просадки.',
        accent: T.green }
    : { icon: '📊', title: 'Начни с 2–3 пар',
        body: 'Запусти ботов на ликвидных парах — BTCUSDT, ETHUSDT. Расширяй по мере роста опыта.',
        accent: T.green }

  const r3: Rec = (a.status === 'positions' || a.status === 'bots')
    ? { icon: '🔗', title: 'Подключи биржу — увидишь всё',
        body: 'После добавления API-ключа система покажет твои позиции и историю. Управляй ими в одном месте.',
        accent: T.blue }
    : { icon: '🔑', title: 'Первый шаг: API-ключ',
        body: 'Создай API-ключ на Bybit с разрешением на торговлю и добавь его в настройки. Займёт 2 минуты.',
        accent: T.blue }

  return [r1, r2, r3]
}

// ─── StepIndicator ─────────────────────────────────────────────────────────────
function StepIndicator({ current, furthest, total, answers, onJump }: {
  current: number; furthest: number; total: number; answers: Answers
  onJump: (i: number) => void
}) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 28 }}>
      {STEPS.map((step, i) => {
        const done = isAnswered(answers, step.key)
        const active = i === current
        const reachable = i <= furthest
        return (
          <button
            key={i}
            onClick={() => reachable && onJump(i)}
            title={reachable ? step.title : undefined}
            style={{
              height: 6,
              width: active ? 28 : 6,
              borderRadius: 3,
              background: active ? T.blue : done ? T.blueBd : T.border,
              border: 'none',
              cursor: reachable ? 'pointer' : 'default',
              padding: 0,
              transition: 'all .3s cubic-bezier(.4,0,.2,1)',
              opacity: reachable ? 1 : 0.35,
            }}
          />
        )
      })}
      <span style={{ fontSize: 11, color: T.dim, marginLeft: 4, letterSpacing: '.8px', textTransform: 'uppercase', fontWeight: 600 }}>
        {current + 1} / {total}
      </span>
    </div>
  )
}

// ─── OptCard ──────────────────────────────────────────────────────────────────
function OptCard({ icon, label, sub, selected, multi, onClick }: {
  icon: string; label: string; sub: string; selected: boolean; multi?: boolean; onClick: () => void
}) {
  const [hov, setHov] = useState(false)
  return (
    <button
      onClick={onClick}
      onMouseEnter={() => setHov(true)}
      onMouseLeave={() => setHov(false)}
      style={{
        background: selected ? T.blueSoft : hov ? 'rgba(255,255,255,.025)' : 'rgba(255,255,255,.015)',
        border: `1px solid ${selected ? T.blueBd : hov ? T.borderHi : T.border}`,
        borderRadius: 12,
        padding: '13px 15px',
        cursor: 'pointer',
        textAlign: 'left',
        transition: 'all .15s',
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        width: '100%',
        outline: 'none',
      }}>
      <span style={{ fontSize: 24, lineHeight: 1, flexShrink: 0 }}>{icon}</span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ ...grotesk, fontSize: 13, fontWeight: 700, color: selected ? T.text : T.body }}>{label}</div>
        <div style={{ fontSize: 11, color: T.dim, marginTop: 2 }}>{sub}</div>
      </div>
      {/* indicator */}
      <div style={{
        width: 18, height: 18, borderRadius: multi ? 4 : '50%', flexShrink: 0,
        background: selected ? T.blue : 'transparent',
        border: `2px solid ${selected ? T.blue : T.border}`,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        transition: 'all .15s',
      }}>
        {selected && (
          <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="#fff" strokeWidth={3.5} strokeLinecap="round" strokeLinejoin="round">
            <path d="M5 13l4 4 10-10" />
          </svg>
        )}
      </div>
    </button>
  )
}

// ─── QuestionView ─────────────────────────────────────────────────────────────
function QuestionView({ step, answers, onSingle, onMultiToggle, onMultiNext }: {
  step: Step
  answers: Answers
  onSingle: (v: string) => void
  onMultiToggle: (v: string) => void
  onMultiNext: () => void
}) {
  const multi = !!step.multi
  const multiSel = multi ? (answers[step.key] as string[]) : []
  const singleSel = !multi ? (answers[step.key] as string | null) : null

  return (
    <div style={{ animation: 'wFadeIn .3s ease-out' }}>
      <h2 style={{ ...grotesk, fontSize: 22, fontWeight: 700, color: T.text, letterSpacing: '-0.4px', margin: '0 0 6px' }}>
        {step.title}
      </h2>
      <p style={{ fontSize: 13, color: T.dim, margin: '0 0 20px', lineHeight: 1.5 }}>{step.sub}</p>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginBottom: 20 }}>
        {step.opts.map(o => (
          <OptCard
            key={o.value}
            icon={o.icon} label={o.label} sub={o.sub}
            multi={multi}
            selected={multi ? multiSel.includes(o.value) : singleSel === o.value}
            onClick={() => multi ? onMultiToggle(o.value) : onSingle(o.value)}
          />
        ))}
      </div>

      {/* "Continue" button for multi-select */}
      {multi && (
        <button
          onClick={onMultiNext}
          disabled={multiSel.length === 0}
          style={{
            width: '100%', padding: '12px', borderRadius: 11,
            background: multiSel.length > 0 ? T.blue : T.border,
            border: 'none', color: multiSel.length > 0 ? '#fff' : T.dim,
            fontSize: 14, fontWeight: 700, cursor: multiSel.length > 0 ? 'pointer' : 'default',
            ...grotesk, letterSpacing: '-0.2px',
            transition: 'all .2s',
            boxShadow: multiSel.length > 0 ? '0 6px 24px -8px rgba(91,140,255,.55)' : 'none',
          }}
        >
          Продолжить {multiSel.length > 0 ? `(${multiSel.length} выбрано)` : ''}
        </button>
      )}
    </div>
  )
}

// ─── RecsView ─────────────────────────────────────────────────────────────────
function RecsView({ answers, onAccounts, onHome }: {
  answers: Answers; onAccounts: () => void; onHome: () => void
}) {
  const recs = buildRecs(answers)
  return (
    <div style={{ animation: 'wFadeIn .35s ease-out' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 13, marginBottom: 24 }}>
        <div style={{
          width: 44, height: 44, borderRadius: 12, background: T.greenSoft,
          border: `1px solid ${T.greenBd}`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
        }}>
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke={T.green} strokeWidth={2.5} strokeLinecap="round" strokeLinejoin="round">
            <path d="M20 6L9 17l-5-5" />
          </svg>
        </div>
        <div>
          <div style={{ ...grotesk, fontSize: 20, fontWeight: 700, color: T.text, letterSpacing: '-0.3px' }}>Анализ готов!</div>
          <div style={{ fontSize: 13, color: T.dim, marginTop: 2 }}>Персональные рекомендации для вас</div>
        </div>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 24 }}>
        {recs.map((rec, i) => (
          <div key={i} style={{
            background: 'rgba(255,255,255,.02)', border: `1px solid ${T.border}`,
            borderRadius: 12, padding: '14px 15px', display: 'flex', gap: 13, alignItems: 'flex-start',
          }}>
            <div style={{
              width: 36, height: 36, borderRadius: 9, flexShrink: 0,
              background: `${rec.accent}18`, border: `1px solid ${rec.accent}35`,
              display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 17,
            }}>{rec.icon}</div>
            <div>
              <div style={{ ...grotesk, fontSize: 13, fontWeight: 700, color: T.text, marginBottom: 3 }}>{rec.title}</div>
              <div style={{ fontSize: 12, color: T.dim, lineHeight: 1.55 }}>{rec.body}</div>
            </div>
          </div>
        ))}
      </div>

      <button onClick={onAccounts} style={{
        width: '100%', padding: '12px', background: T.blue, border: 'none', borderRadius: 11,
        color: '#fff', fontSize: 14, fontWeight: 700, cursor: 'pointer', ...grotesk,
        letterSpacing: '-0.2px', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 8,
        boxShadow: '0 8px 24px -8px rgba(91,140,255,.6)', transition: 'transform .15s, box-shadow .15s',
      }}
        onMouseEnter={e => { e.currentTarget.style.transform = 'translateY(-1px)'; e.currentTarget.style.boxShadow = '0 12px 28px -8px rgba(91,140,255,.72)' }}
        onMouseLeave={e => { e.currentTarget.style.transform = ''; e.currentTarget.style.boxShadow = '0 8px 24px -8px rgba(91,140,255,.6)' }}
      >
        Подключить биржу
      </button>
      <button onClick={onHome} style={{
        width: '100%', padding: '10px', background: 'transparent', border: 'none',
        color: T.dim, fontSize: 13, cursor: 'pointer', marginTop: 6, fontFamily: 'inherit',
        borderRadius: 9, transition: 'color .15s',
      }}
        onMouseEnter={e => (e.currentTarget.style.color = T.body)}
        onMouseLeave={e => (e.currentTarget.style.color = T.dim)}
      >
        Позже, перейти на дашборд
      </button>
    </div>
  )
}

// ─── Page ─────────────────────────────────────────────────────────────────────
export function WelcomePage() {
  const navigate = useNavigate()
  const [answers, setAnswers] = useState<Answers>(load)
  const [loaded, setLoaded] = useState(false)

  // Determine starting step: first unanswered, or recs page
  const [stepIdx, setStepIdx] = useState<number>(() => {
    const saved = load()
    for (let i = 0; i < STEPS.length; i++) {
      if (!isAnswered(saved, STEPS[i].key)) return i
    }
    return STEPS.length
  })
  // Track furthest reached step so user can jump back to any answered step
  const [furthestStep, setFurthestStep] = useState(stepIdx)

  useEffect(() => {
    listAccounts()
      .then(accs => {
        if (accs.length > 0) navigate('/', { replace: true })
        else setLoaded(true)
      })
      .catch(() => setLoaded(true))
  }, [navigate])

  const advance = () => {
    const next = stepIdx + 1
    setStepIdx(next)
    setFurthestStep(f => Math.max(f, next))
  }

  const handleSingle = (value: string) => {
    const next = { ...answers, [STEPS[stepIdx].key]: value }
    setAnswers(next)
    save(next)
    setTimeout(advance, 240)
  }

  const handleMultiToggle = (value: string) => {
    const key = STEPS[stepIdx].key
    const cur = answers[key] as string[]
    const next = cur.includes(value) ? cur.filter(v => v !== value) : [...cur, value]
    const updated = { ...answers, [key]: next }
    setAnswers(updated)
    save(updated)
  }

  const handleMultiNext = () => {
    const key = STEPS[stepIdx].key
    if ((answers[key] as string[]).length > 0) advance()
  }

  const jumpTo = (i: number) => {
    if (i <= furthestStep) setStepIdx(i)
  }

  const isRecs = stepIdx >= STEPS.length
  const step = STEPS[stepIdx]

  // ── Modal backdrop — fixed over entire viewport, blocks sidebar/nav ──────────
  const backdropStyle: CSSProperties = {
    position: 'fixed',
    inset: 0,
    zIndex: 1000,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    padding: '24px',
    background: 'rgba(8, 11, 18, 0.72)',
    backdropFilter: 'blur(3px)',
  }

  if (!loaded) {
    return (
      <div style={backdropStyle}>
        <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke={T.blue} strokeWidth={2} strokeLinecap="round" style={{ animation: 'spin 1s linear infinite' }}>
          <path d="M3 12a9 9 0 1 0 3-6.7" /><path d="M3 4v5h5" />
        </svg>
        <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
      </div>
    )
  }

  return (
    <div style={backdropStyle}>
      {/* Card */}
      <div style={{
        width: '100%', maxWidth: 540,
        background: T.panel,
        border: `1px solid ${T.border}`,
        borderRadius: 20,
        padding: '32px 36px',
        boxShadow: '0 32px 80px -16px rgba(0,0,0,.8)',
        maxHeight: '90vh',
        overflowY: 'auto',
        position: 'relative',
      }}>
        {/* Top row: logo + skip */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 28 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
            <div style={{ width: 28, height: 28, background: 'linear-gradient(135deg,#5b8cff,#c14dff)', borderRadius: 7, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <span style={{ color: '#fff', fontSize: 12, fontWeight: 800, ...grotesk }}>N</span>
            </div>
            <span style={{ ...grotesk, color: T.text, fontWeight: 700, fontSize: 15, letterSpacing: '-0.2px' }}>NovaBot</span>
          </div>
          {!isRecs && (
            <button onClick={() => { markDone(); navigate('/', { replace: true }) }} style={{
              background: 'transparent', border: 'none', color: T.dim, cursor: 'pointer',
              fontSize: 12, display: 'flex', alignItems: 'center', gap: 4, padding: '5px 10px',
              borderRadius: 7, fontFamily: 'inherit', transition: 'color .15s',
            }}
              onMouseEnter={e => (e.currentTarget.style.color = T.body)}
              onMouseLeave={e => (e.currentTarget.style.color = T.dim)}
            >
              Пропустить
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
                <path d="M5 12h14M12 5l7 7-7 7" />
              </svg>
            </button>
          )}
        </div>

        {/* Step indicator dots */}
        {!isRecs && (
          <StepIndicator
            current={stepIdx}
            furthest={furthestStep}
            total={STEPS.length}
            answers={answers}
            onJump={jumpTo}
          />
        )}

        {/* Content */}
        <div key={stepIdx}>
          {isRecs
            ? <RecsView answers={answers} onAccounts={() => { markDone(); navigate('/accounts') }} onHome={() => { markDone(); navigate('/', { replace: true }) }} />
            : <QuestionView
                step={step}
                answers={answers}
                onSingle={handleSingle}
                onMultiToggle={handleMultiToggle}
                onMultiNext={handleMultiNext}
              />
          }
        </div>

        {/* Back button */}
        {!isRecs && stepIdx > 0 && (
          <button onClick={() => setStepIdx(i => i - 1)} style={{
            background: 'transparent', border: 'none', color: T.dim,
            cursor: 'pointer', fontSize: 12, padding: '10px 0 0', marginTop: 4,
            display: 'inline-flex', alignItems: 'center', gap: 5, fontFamily: 'inherit',
            transition: 'color .15s',
          }}
            onMouseEnter={e => (e.currentTarget.style.color = T.body)}
            onMouseLeave={e => (e.currentTarget.style.color = T.dim)}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
              <path d="M19 12H5M12 5l-7 7 7 7" />
            </svg>
            Назад
          </button>
        )}
      </div>

      <style>{`
        @keyframes wFadeIn { from { opacity: 0; transform: translateY(12px) } to { opacity: 1; transform: translateY(0) } }
        @keyframes spin    { to   { transform: rotate(360deg) } }
      `}</style>
    </div>
  )
}
