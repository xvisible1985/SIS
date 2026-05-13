import { useState, FormEvent } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { register } from '../api/auth'
import { useAuth } from '../hooks/useAuth'

export function RegisterPage() {
  const navigate = useNavigate()
  const { login: authLogin } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res = await register(email, password)
      authLogin(res.token, res.user_id, res.email)
      navigate('/')
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Registration failed'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center font-sans" style={{ background: '#080b12' }}>
      <div className="w-full max-w-[360px] px-4">

        {/* logo */}
        <div className="flex items-center justify-center gap-2.5 mb-8">
          <div className="w-8 h-8 rounded-[8px] bg-[linear-gradient(135deg,#5b8cff,#7b5bff)] flex items-center justify-center shrink-0">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M3 17l4-6 4 4 4-8 6 10"/>
            </svg>
          </div>
          <span className="text-[20px] font-bold text-white tracking-[-0.4px]">Novabot</span>
        </div>

        {/* card */}
        <div className="rounded-[16px] p-6" style={{ background: '#0d1320', border: '1px solid rgba(255,255,255,.08)' }}>
          <h2 className="text-[15px] font-semibold text-white mb-1">Создать аккаунт</h2>
          <p className="text-[12px] text-[#5b6479] mb-5">Заполните данные для регистрации</p>

          <form onSubmit={handleSubmit} className="flex flex-col gap-3">
            <div className="flex flex-col gap-1.5">
              <label className="text-[11px] text-[#7b8aa6] font-medium uppercase tracking-[.7px]">Email</label>
              <input
                type="email"
                placeholder="you@example.com"
                value={email}
                onChange={e => setEmail(e.target.value)}
                required
                className="w-full rounded-[9px] px-3 py-2.5 text-[13px] text-white placeholder-[#3d4a63] outline-none transition-colors"
                style={{ background: 'rgba(255,255,255,.05)', border: '1px solid rgba(255,255,255,.09)' }}
                onFocus={e => e.currentTarget.style.borderColor = 'rgba(91,140,255,.5)'}
                onBlur={e => e.currentTarget.style.borderColor = 'rgba(255,255,255,.09)'}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <label className="text-[11px] text-[#7b8aa6] font-medium uppercase tracking-[.7px]">Пароль</label>
              <input
                type="password"
                placeholder="••••••••"
                value={password}
                onChange={e => setPassword(e.target.value)}
                required
                minLength={8}
                className="w-full rounded-[9px] px-3 py-2.5 text-[13px] text-white placeholder-[#3d4a63] outline-none transition-colors"
                style={{ background: 'rgba(255,255,255,.05)', border: '1px solid rgba(255,255,255,.09)' }}
                onFocus={e => e.currentTarget.style.borderColor = 'rgba(91,140,255,.5)'}
                onBlur={e => e.currentTarget.style.borderColor = 'rgba(255,255,255,.09)'}
              />
              <span className="text-[10px] text-[#3d4a63]">Минимум 8 символов</span>
            </div>

            {error && (
              <div className="rounded-[8px] px-3 py-2 text-[12px] text-rose-300" style={{ background: 'rgba(248,113,113,.10)', border: '1px solid rgba(248,113,113,.20)' }}>
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={loading}
              className="w-full rounded-[9px] py-2.5 text-[13px] font-semibold text-white transition-opacity disabled:opacity-50 mt-1"
              style={{ background: 'linear-gradient(135deg,#5b8cff,#7b5bff)' }}
            >
              {loading ? 'Создаём…' : 'Зарегистрироваться'}
            </button>
          </form>
        </div>

        <p className="mt-4 text-center text-[12px] text-[#5b6479]">
          Уже есть аккаунт?{' '}
          <Link to="/login" className="text-[#5b8cff] hover:text-[#7ba8ff] transition-colors">
            Войти
          </Link>
        </p>
      </div>
    </div>
  )
}
