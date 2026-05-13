import { useState, FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { login, register } from '../api/auth'
import { useAuth } from '../hooks/useAuth'

type Tab = 'login' | 'register'

interface Props {
  defaultTab?: Tab
}

function strengthChecks(pw: string) {
  return {
    length:  pw.length >= 8,
    digit:   /\d/.test(pw),
    upper:   /[A-Z]/.test(pw),
    special: /[^A-Za-z0-9]/.test(pw),
  }
}

function strengthScore(pw: string): number {
  const c = strengthChecks(pw)
  return [c.length, c.digit, c.upper, c.special].filter(Boolean).length
}

export function AuthPage({ defaultTab = 'login' }: Props) {
  const navigate = useNavigate()
  const { login: authLogin } = useAuth()
  const [tab, setTab] = useState<Tab>(defaultTab)
  const [email, setEmail]       = useState('')
  const [password, setPassword] = useState('')
  const [showPw, setShowPw]     = useState(false)
  const [agreed, setAgreed]     = useState(false)
  const [remember, setRemember] = useState(true)
  const [error, setError]       = useState('')
  const [loading, setLoading]   = useState(false)

  const checks = strengthChecks(password)
  const score  = strengthScore(password)
  const scoreColor = score <= 1 ? '#ef4444' : score === 2 ? '#f59e0b' : score === 3 ? '#5b8cff' : '#5be0a0'

  function switchTab(t: Tab) {
    setTab(t)
    setError('')
    setPassword('')
    setShowPw(false)
    setAgreed(false)
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (tab === 'register' && !agreed) { setError('Примите условия использования'); return }
    setError('')
    setLoading(true)
    try {
      const res = tab === 'login'
        ? await login(email, password)
        : await register(email, password)
      authLogin(res.token, res.user_id, res.email, tab === 'login' ? remember : true)
      navigate('/')
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : tab === 'login' ? 'Ошибка входа' : 'Ошибка регистрации')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center font-sans px-4" style={{ background: '#080b12' }}>
      <div className="w-full max-w-[420px]">

        {/* card — everything inside */}
        <div className="rounded-[20px] p-6 flex flex-col gap-4" style={{ background: '#0d1320', border: '1px solid rgba(255,255,255,.08)', boxShadow: '0 24px 64px rgba(0,0,0,.5)' }}>

          {/* header */}
          <div className="flex items-center gap-2.5">
            <div className="w-9 h-9 rounded-[10px] bg-[linear-gradient(135deg,#5b8cff,#7b5bff)] flex items-center justify-center shrink-0">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M3 17l4-6 4 4 4-8 6 10"/>
              </svg>
            </div>
            <span className="text-[17px] font-bold text-white tracking-[-0.3px]">NovaBot</span>
            <span className="px-1.5 py-0.5 rounded-[4px] text-[9px] font-bold uppercase tracking-[.8px] text-[#5b8cff]" style={{ background: 'rgba(91,140,255,.15)', border: '1px solid rgba(91,140,255,.3)' }}>BETA</span>
          </div>

          <div>
            <h1 className="text-[20px] font-bold text-white tracking-[-0.4px] mb-1">
              {tab === 'login' ? 'Войти в аккаунт' : 'Создать аккаунт'}
            </h1>
            <p className="text-[13px] text-[#5b6479]">Подключайте биржи, запускайте стратегии и роботов.</p>
          </div>

          {/* tabs */}
          <div className="flex rounded-[10px] p-1" style={{ background: 'rgba(255,255,255,.05)', border: '1px solid rgba(255,255,255,.07)' }}>
            {(['login', 'register'] as Tab[]).map(t => (
              <button
                key={t}
                type="button"
                onClick={() => switchTab(t)}
                className="flex-1 py-2 rounded-[7px] text-[13px] font-semibold transition-all"
                style={tab === t
                  ? { background: 'linear-gradient(135deg,#5b8cff,#7b5bff)', color: 'white', boxShadow: '0 2px 12px rgba(91,140,255,.35)' }
                  : { color: '#5b6479' }
                }
              >
                {t === 'login' ? 'Вход' : 'Регистрация'}
              </button>
            ))}
          </div>

          <form onSubmit={handleSubmit} className="flex flex-col gap-3.5">

            {/* email */}
            <div className="flex flex-col gap-1.5">
              <label className="text-[10px] font-bold uppercase tracking-[.9px] text-[#7b8aa6]">Email</label>
              <div className="relative">
                <svg className="absolute left-3 top-1/2 -translate-y-1/2 shrink-0" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#3d4a63" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <rect x="2" y="4" width="20" height="16" rx="2"/><path d="m2 7 10 7 10-7"/>
                </svg>
                <input
                  type="email"
                  placeholder="you@example.com"
                  value={email}
                  onChange={e => setEmail(e.target.value)}
                  required
                  className="w-full rounded-[9px] pl-9 pr-3 py-2.5 text-[13px] text-white placeholder-[#3d4a63] outline-none transition-colors"
                  style={{ background: 'rgba(255,255,255,.05)', border: '1px solid rgba(255,255,255,.09)' }}
                  onFocus={e => e.currentTarget.style.borderColor = 'rgba(91,140,255,.5)'}
                  onBlur={e => e.currentTarget.style.borderColor = 'rgba(255,255,255,.09)'}
                />
              </div>
            </div>

            {/* password */}
            <div className="flex flex-col gap-1.5">
              <label className="text-[10px] font-bold uppercase tracking-[.9px] text-[#7b8aa6]">Пароль</label>
              <div className="relative">
                <svg className="absolute left-3 top-1/2 -translate-y-1/2 shrink-0" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#3d4a63" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
                </svg>
                <input
                  type={showPw ? 'text' : 'password'}
                  placeholder="Минимум 8 символов"
                  value={password}
                  onChange={e => setPassword(e.target.value)}
                  required
                  minLength={tab === 'register' ? 8 : undefined}
                  className="w-full rounded-[9px] pl-9 pr-10 py-2.5 text-[13px] text-white placeholder-[#3d4a63] outline-none transition-colors"
                  style={{ background: 'rgba(255,255,255,.05)', border: '1px solid rgba(255,255,255,.09)' }}
                  onFocus={e => e.currentTarget.style.borderColor = 'rgba(91,140,255,.5)'}
                  onBlur={e => e.currentTarget.style.borderColor = 'rgba(255,255,255,.09)'}
                />
                <button type="button" onClick={() => setShowPw(v => !v)}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-[#3d4a63] hover:text-[#7b8aa6] transition-colors">
                  {showPw
                    ? <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                    : <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
                  }
                </button>
              </div>

              {/* strength — only on register */}
              {tab === 'register' && password.length > 0 && (
                <div className="flex flex-col gap-2 mt-0.5">
                  {/* bar */}
                  <div className="flex gap-1">
                    {[0,1,2,3].map(i => (
                      <div key={i} className="flex-1 h-[3px] rounded-full transition-all" style={{ background: i < score ? scoreColor : 'rgba(255,255,255,.08)' }} />
                    ))}
                  </div>
                  {/* checklist */}
                  <div className="grid grid-cols-2 gap-x-3 gap-y-1">
                    {([
                      [checks.length,  '8+ символов'],
                      [checks.digit,   'Цифра'],
                      [checks.upper,   'Заглавная'],
                      [checks.special, 'Спецсимвол'],
                    ] as [boolean, string][]).map(([ok, label]) => (
                      <span key={label} className="flex items-center gap-1.5 text-[11px] transition-colors" style={{ color: ok ? '#5be0a0' : '#3d4a63' }}>
                        <span className="w-3 h-3 rounded-full flex items-center justify-center shrink-0" style={{ background: ok ? 'rgba(91,224,160,.2)' : 'rgba(255,255,255,.06)' }}>
                          {ok && <svg width="7" height="7" viewBox="0 0 12 12" fill="none"><path d="M2 6l3 3 5-5" stroke="#5be0a0" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/></svg>}
                        </span>
                        {label}
                      </span>
                    ))}
                  </div>
                </div>
              )}
            </div>

            {/* remember me — login only */}
            {tab === 'login' && (
              <button type="button" onClick={() => setRemember(v => !v)} className="flex items-center gap-2.5 w-fit">
                <div
                  className="w-4 h-4 rounded-[4px] flex items-center justify-center shrink-0 transition-colors"
                  style={remember
                    ? { background: 'linear-gradient(135deg,#5b8cff,#7b5bff)', border: '1px solid transparent' }
                    : { background: 'rgba(255,255,255,.05)', border: '1px solid rgba(255,255,255,.15)' }
                  }
                >
                  {remember && <svg width="9" height="9" viewBox="0 0 12 12" fill="none"><path d="M2 6l3 3 5-5" stroke="white" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/></svg>}
                </div>
                <span className="text-[12px] text-[#7b8aa6] select-none">Запомнить меня</span>
              </button>
            )}

            {/* terms — register only */}
            {tab === 'register' && (
              <button type="button" onClick={() => setAgreed(v => !v)} className="flex items-start gap-2.5 w-fit text-left">
                <div
                  className="mt-0.5 w-4 h-4 rounded-[4px] flex items-center justify-center shrink-0 transition-colors"
                  style={agreed
                    ? { background: 'linear-gradient(135deg,#5b8cff,#7b5bff)', border: '1px solid transparent' }
                    : { background: 'rgba(255,255,255,.05)', border: '1px solid rgba(255,255,255,.15)' }
                  }
                >
                  {agreed && <svg width="9" height="9" viewBox="0 0 12 12" fill="none"><path d="M2 6l3 3 5-5" stroke="white" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/></svg>}
                </div>
                <span className="text-[12px] text-[#7b8aa6] leading-snug select-none">
                  Принимаю{' '}
                  <span className="text-[#5b8cff]">условия</span>
                  {' '}и{' '}
                  <span className="text-[#5b8cff]">политику конфиденциальности</span>
                </span>
              </button>
            )}

            {error && (
              <div className="rounded-[8px] px-3 py-2 text-[12px] text-rose-300" style={{ background: 'rgba(248,113,113,.10)', border: '1px solid rgba(248,113,113,.20)' }}>
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={loading}
              className="w-full rounded-[10px] py-3 text-[14px] font-semibold text-white transition-opacity disabled:opacity-50 flex items-center justify-center gap-2"
              style={{ background: 'linear-gradient(135deg,#5b8cff,#7b5bff)', boxShadow: '0 4px 20px rgba(91,140,255,.35)' }}
            >
              {loading
                ? (tab === 'login' ? 'Входим…' : 'Создаём…')
                : (tab === 'login' ? 'Войти' : 'Создать аккаунт')
              }
              {!loading && <span style={{ opacity: 0.8 }}>→</span>}
            </button>
          </form>

          {/* social */}
          <div className="flex items-center gap-3">
            <div className="flex-1 h-px" style={{ background: 'rgba(255,255,255,.07)' }} />
            <span className="text-[10px] font-semibold uppercase tracking-[.8px] text-[#3d4a63]">или быстрее</span>
            <div className="flex-1 h-px" style={{ background: 'rgba(255,255,255,.07)' }} />
          </div>
          <div className="grid grid-cols-3 gap-2">
            {[
              { label: 'Google', icon: <svg width="14" height="14" viewBox="0 0 24 24"><path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4"/><path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/><path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l3.66-2.84z" fill="#FBBC05"/><path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/></svg> },
              { label: 'Telegram', icon: <svg width="14" height="14" viewBox="0 0 24 24" fill="#2AABEE"><path d="M12 0C5.373 0 0 5.373 0 12s5.373 12 12 12 12-5.373 12-12S18.627 0 12 0zm5.894 8.221-1.97 9.28c-.145.658-.537.818-1.084.508l-3-2.21-1.447 1.394c-.16.16-.295.295-.605.295l.213-3.053 5.56-5.023c.242-.213-.054-.333-.373-.12l-6.871 4.326-2.962-.924c-.643-.204-.657-.643.136-.953l11.57-4.461c.537-.194 1.006.131.833.941z"/></svg> },
              { label: 'Apple', icon: <svg width="14" height="14" viewBox="0 0 24 24" fill="white"><path d="M18.71 19.5c-.83 1.24-1.71 2.45-3.05 2.47-1.34.03-1.77-.79-3.29-.79-1.53 0-2 .77-3.27.82-1.31.05-2.3-1.32-3.14-2.53C4.25 17 2.94 12.45 4.7 9.39c.87-1.52 2.43-2.48 4.12-2.51 1.28-.02 2.5.87 3.29.87.78 0 2.26-1.07 3.8-.91.65.03 2.47.26 3.64 1.98-.09.06-2.17 1.28-2.15 3.81.03 3.02 2.65 4.03 2.68 4.04-.03.07-.42 1.44-1.38 2.83M13 3.5c.73-.83 1.94-1.46 2.94-1.5.13 1.17-.34 2.35-1.04 3.19-.69.85-1.83 1.51-2.95 1.42-.15-1.15.41-2.35 1.05-3.11z"/></svg> },
            ].map(({ label, icon }) => (
              <button
                key={label}
                type="button"
                title={`${label} (скоро)`}
                disabled
                className="flex items-center justify-center gap-1.5 rounded-[9px] py-2.5 text-[12px] font-medium text-[#5b6479] cursor-not-allowed"
                style={{ background: 'rgba(255,255,255,.04)', border: '1px solid rgba(255,255,255,.08)' }}
              >
                {icon}
                {label}
              </button>
            ))}
          </div>

          <p className="text-center text-[12px] text-[#5b6479] pt-1">
            {tab === 'login' ? (
              <>Нет аккаунта? <button type="button" onClick={() => switchTab('register')} className="text-[#5b8cff] hover:text-[#7ba8ff] transition-colors">Создать →</button></>
            ) : (
              <>Уже есть аккаунт? <button type="button" onClick={() => switchTab('login')} className="text-[#5b8cff] hover:text-[#7ba8ff] transition-colors">Войти →</button></>
            )}
          </p>

        </div>
      </div>
    </div>
  )
}
