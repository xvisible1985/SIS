// frontend/src/pages/AccountPage.tsx
import { useState, useEffect } from 'react'
import { useAuth } from '../hooks/useAuth'
import {
  getProfile, updateProfile, changePassword, getTelegramLink,
  disconnectTelegram, getNotifications, updateNotifications, getReferral,
  type AccountProfile, type NotificationSettings, type ReferralInfo,
} from '../api/account'

type Tab = 'profile' | 'billing' | 'integrations' | 'referrals'

const TABS: { id: Tab; label: string }[] = [
  { id: 'profile', label: 'Профиль' },
  { id: 'billing', label: 'Биллинг' },
  { id: 'integrations', label: 'Интеграции' },
  { id: 'referrals', label: 'Рефералы' },
]

export function AccountPage() {
  const { email } = useAuth()
  const [tab, setTab] = useState<Tab>('profile')
  const [profile, setProfile] = useState<AccountProfile | null>(null)
  const [notifications, setNotifications] = useState<NotificationSettings | null>(null)
  const [referral, setReferral] = useState<ReferralInfo | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    getProfile()
      .then(setProfile)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : 'Ошибка загрузки'))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    if (tab === 'integrations' && notifications === null) {
      getNotifications().then(setNotifications).catch(() => {})
    }
    if (tab === 'referrals' && referral === null) {
      getReferral().then(setReferral).catch(() => {})
    }
  }, [tab])

  if (loading) {
    return (
      <div className="flex h-full items-center justify-center text-slate-500 text-sm">
        Загрузка...
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-6 text-rose-400 text-sm">{error}</div>
    )
  }

  const initials = (profile?.username ?? email ?? '').slice(0, 2).toUpperCase() || '??'
  const displayName = profile?.username ?? email?.split('@')[0] ?? ''

  return (
    <div className="p-6 max-w-2xl">
      <div className="mb-5">
        <h1 className="text-lg font-semibold text-slate-100">Аккаунт</h1>
        <p className="text-sm text-slate-500 mt-0.5">Управление профилем и настройками</p>
      </div>

      {/* Tab bar */}
      <div className="flex gap-1 border-b border-white/[.08] mb-6">
        {TABS.map(t => (
          <button
            key={t.id}
            type="button"
            onClick={() => setTab(t.id)}
            className={
              'px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ' +
              (tab === t.id
                ? 'border-[#5b8cff] text-[#5b8cff]'
                : 'border-transparent text-slate-500 hover:text-slate-300')
            }
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === 'profile' && profile && (
        <ProfileTab
          profile={profile}
          initials={initials}
          displayName={displayName}
          onProfileUpdate={setProfile}
        />
      )}
      {tab === 'billing' && profile && <BillingTab plan={profile.plan} />}
      {tab === 'integrations' && profile && notifications !== null && (
        <IntegrationsTab
          profile={profile}
          notifications={notifications}
          onProfileUpdate={setProfile}
          onNotificationsUpdate={setNotifications}
        />
      )}
      {tab === 'integrations' && notifications === null && (
        <div className="text-slate-500 text-sm">Загрузка...</div>
      )}
      {tab === 'referrals' && referral !== null && <ReferralsTab referral={referral} />}
      {tab === 'referrals' && referral === null && (
        <div className="text-slate-500 text-sm">Загрузка...</div>
      )}
    </div>
  )
}

// ─── Profile Tab ──────────────────────────────────────────────────────────────

function ProfileTab({
  profile,
  initials,
  displayName,
  onProfileUpdate,
}: {
  profile: AccountProfile
  initials: string
  displayName: string
  onProfileUpdate: (p: AccountProfile) => void
}) {
  const [username, setUsername] = useState(profile.username ?? '')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')

  const [curPwd, setCurPwd] = useState('')
  const [newPwd, setNewPwd] = useState('')
  const [confirmPwd, setConfirmPwd] = useState('')
  const [pwdSaving, setPwdSaving] = useState(false)
  const [pwdError, setPwdError] = useState('')
  const [pwdSuccess, setPwdSuccess] = useState(false)

  async function handleSaveUsername() {
    setSaveError('')
    setSaving(true)
    try {
      const updated = await updateProfile(username.trim())
      onProfileUpdate(updated)
    } catch (e: unknown) {
      setSaveError(e instanceof Error ? e.message : 'Ошибка')
    } finally {
      setSaving(false)
    }
  }

  async function handleChangePassword() {
    setPwdError('')
    setPwdSuccess(false)
    if (newPwd !== confirmPwd) {
      setPwdError('Пароли не совпадают')
      return
    }
    if (newPwd.length < 8) {
      setPwdError('Минимум 8 символов')
      return
    }
    setPwdSaving(true)
    try {
      await changePassword(curPwd, newPwd)
      setPwdSuccess(true)
      setCurPwd('')
      setNewPwd('')
      setConfirmPwd('')
    } catch (e: unknown) {
      setPwdError(e instanceof Error ? e.message : 'Ошибка')
    } finally {
      setPwdSaving(false)
    }
  }

  return (
    <div className="flex flex-col gap-4 max-w-[480px]">
      {/* User card */}
      <div className="flex items-center gap-4 bg-white/[.04] border border-white/[.08] rounded-xl p-4">
        <div className="w-12 h-12 rounded-full bg-[#5b8cff] flex items-center justify-center text-lg font-bold text-white shrink-0">
          {initials}
        </div>
        <div>
          <div className="text-sm font-semibold text-slate-100">{displayName}</div>
          <div className="text-xs text-slate-500">{profile.email}</div>
        </div>
      </div>

      {/* Email (read-only) */}
      <div>
        <label className="block text-[11px] font-medium text-slate-400 uppercase tracking-wide mb-1.5">
          Email
        </label>
        <div className="bg-white/[.04] border border-white/[.08] rounded-lg px-3.5 py-2.5 text-sm text-slate-400">
          {profile.email}
        </div>
      </div>

      {/* Username */}
      <div>
        <label className="block text-[11px] font-medium text-slate-400 uppercase tracking-wide mb-1.5">
          Имя пользователя
        </label>
        <div className="flex gap-2">
          <input
            type="text"
            value={username}
            onChange={e => setUsername(e.target.value)}
            placeholder="3–30 символов, a–z 0–9 _"
            className="flex-1 bg-white/[.06] border border-white/[.12] rounded-lg px-3.5 py-2.5 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-[#5b8cff]/60"
          />
          <button
            type="button"
            onClick={handleSaveUsername}
            disabled={saving || !username.trim()}
            className="px-4 bg-[#5b8cff] rounded-lg text-sm font-medium text-white disabled:opacity-40 hover:brightness-110 transition-all"
          >
            {saving ? '...' : 'Сохранить'}
          </button>
        </div>
        {saveError && <p className="text-xs text-rose-400 mt-1">{saveError}</p>}
      </div>

      {/* Change password */}
      <div className="bg-white/[.04] border border-white/[.08] rounded-xl p-4">
        <p className="text-sm font-medium text-slate-100 mb-3">Смена пароля</p>
        <div className="flex flex-col gap-2">
          <input
            type="password"
            value={curPwd}
            onChange={e => setCurPwd(e.target.value)}
            placeholder="Текущий пароль"
            className="bg-white/[.06] border border-white/[.12] rounded-lg px-3.5 py-2.5 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-[#5b8cff]/60"
          />
          <input
            type="password"
            value={newPwd}
            onChange={e => setNewPwd(e.target.value)}
            placeholder="Новый пароль"
            className="bg-white/[.06] border border-white/[.12] rounded-lg px-3.5 py-2.5 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-[#5b8cff]/60"
          />
          <input
            type="password"
            value={confirmPwd}
            onChange={e => setConfirmPwd(e.target.value)}
            placeholder="Повторите новый пароль"
            className="bg-white/[.06] border border-white/[.12] rounded-lg px-3.5 py-2.5 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-[#5b8cff]/60"
          />
          {pwdError && <p className="text-xs text-rose-400">{pwdError}</p>}
          {pwdSuccess && <p className="text-xs text-emerald-400">Пароль изменён</p>}
          <button
            type="button"
            onClick={handleChangePassword}
            disabled={pwdSaving || !curPwd || !newPwd || !confirmPwd}
            className="py-2.5 bg-white/[.06] border border-white/[.1] rounded-lg text-sm font-medium text-slate-300 disabled:opacity-40 hover:bg-white/[.08] transition-colors"
          >
            {pwdSaving ? '...' : 'Изменить пароль'}
          </button>
        </div>
      </div>
    </div>
  )
}

// ─── Billing Tab ──────────────────────────────────────────────────────────────

function BillingTab({ plan }: { plan: string }) {
  const planLabel = plan === 'pro' ? 'Pro' : plan === 'enterprise' ? 'Enterprise' : 'Free'

  return (
    <div className="flex flex-col gap-4 max-w-[560px]">
      {/* Balance card */}
      <div className="bg-[#5b8cff]/[.08] border border-[#5b8cff]/20 rounded-xl p-5 flex justify-between items-center">
        <div>
          <p className="text-[11px] font-medium text-slate-400 uppercase tracking-wide mb-1">
            Баланс Novabot
          </p>
          <p className="text-3xl font-bold text-slate-100">$0.00</p>
        </div>
        <button
          type="button"
          className="px-5 py-2.5 bg-[#5b8cff] rounded-lg text-sm font-semibold text-white hover:brightness-110 transition-all"
        >
          Пополнить
        </button>
      </div>

      {/* Current plan */}
      <div className="bg-white/[.04] border border-white/[.08] rounded-xl p-4 flex justify-between items-start">
        <div>
          <p className="text-[11px] font-medium text-slate-400 uppercase tracking-wide mb-1.5">
            Текущий план
          </p>
          <p className="text-base font-semibold text-slate-100">{planLabel}</p>
          <p className="text-xs text-slate-500 mt-0.5">Следующее списание: —</p>
        </div>
        <button
          type="button"
          className="px-3.5 py-2 bg-white/[.06] border border-white/[.1] rounded-lg text-xs text-slate-400 hover:bg-white/[.08] transition-colors"
        >
          Сменить план
        </button>
      </div>

      {/* Transaction history */}
      <div className="bg-white/[.04] border border-white/[.08] rounded-xl p-4">
        <p className="text-sm font-medium text-slate-100 mb-3">История транзакций</p>
        <div className="text-center py-6 text-sm text-slate-500">Транзакций пока нет</div>
      </div>
    </div>
  )
}

// ─── Integrations Tab ─────────────────────────────────────────────────────────

function IntegrationsTab({
  profile,
  notifications,
  onProfileUpdate,
  onNotificationsUpdate,
}: {
  profile: AccountProfile
  notifications: NotificationSettings
  onProfileUpdate: (p: AccountProfile) => void
  onNotificationsUpdate: (n: NotificationSettings) => void
}) {
  const [connecting, setConnecting] = useState(false)
  const [disconnecting, setDisconnecting] = useState(false)
  const [connectError, setConnectError] = useState('')
  const [polling, setPolling] = useState(false)

  async function handleConnect() {
    setConnectError('')
    setConnecting(true)
    try {
      const { url } = await getTelegramLink()
      window.open(url, '_blank')
      // Poll for connection up to 30 seconds
      setPolling(true)
      let attempts = 0
      const interval = setInterval(async () => {
        attempts++
        try {
          const updated = await getProfile()
          if (updated.telegram_username) {
            clearInterval(interval)
            setPolling(false)
            onProfileUpdate(updated)
          }
        } catch {
          // ignore
        }
        if (attempts >= 10) {
          clearInterval(interval)
          setPolling(false)
        }
      }, 3000)
    } catch (e: unknown) {
      setConnectError(e instanceof Error ? e.message : 'Ошибка')
    } finally {
      setConnecting(false)
    }
  }

  async function handleDisconnect() {
    setDisconnecting(true)
    try {
      await disconnectTelegram()
      onProfileUpdate({ ...profile, telegram_username: null })
    } catch {
      // ignore
    } finally {
      setDisconnecting(false)
    }
  }

  async function handleToggle(key: keyof NotificationSettings) {
    const updated = { ...notifications, [key]: !notifications[key] }
    onNotificationsUpdate(updated)
    try {
      await updateNotifications({ [key]: updated[key] })
    } catch {
      onNotificationsUpdate(notifications) // revert
    }
  }

  const isConnected = Boolean(profile.telegram_username)

  return (
    <div className="flex flex-col gap-4 max-w-[480px]">
      {/* Telegram block */}
      <div className="bg-white/[.04] border border-white/[.08] rounded-xl p-4">
        <div className="flex items-center gap-2 mb-3">
          <p className="text-sm font-medium text-slate-100">Telegram</p>
          {isConnected && (
            <span className="text-[10px] font-semibold text-emerald-400 bg-emerald-400/10 px-1.5 py-0.5 rounded-full">
              Подключён
            </span>
          )}
        </div>

        {isConnected ? (
          <div className="flex items-center justify-between">
            <p className="text-sm text-slate-300">@{profile.telegram_username}</p>
            <button
              type="button"
              onClick={handleDisconnect}
              disabled={disconnecting}
              className="px-3 py-1.5 text-xs text-rose-400 bg-rose-400/[.08] border border-rose-400/20 rounded-lg hover:bg-rose-400/[.12] transition-colors disabled:opacity-40"
            >
              {disconnecting ? '...' : 'Отключить'}
            </button>
          </div>
        ) : (
          <div>
            <p className="text-xs text-slate-500 mb-3">
              Подключите бота для получения уведомлений о сделках и сигналах.
            </p>
            <button
              type="button"
              onClick={handleConnect}
              disabled={connecting || polling}
              className="px-4 py-2 bg-[#5b8cff] rounded-lg text-sm font-medium text-white hover:brightness-110 transition-all disabled:opacity-40"
            >
              {polling ? 'Ожидание подключения...' : connecting ? '...' : 'Подключить Telegram'}
            </button>
            {connectError && <p className="text-xs text-rose-400 mt-2">{connectError}</p>}
          </div>
        )}
      </div>

      {/* Notification settings — only when connected */}
      {isConnected && (
        <div className="bg-white/[.04] border border-white/[.08] rounded-xl p-4">
          <p className="text-sm font-medium text-slate-100 mb-3">Уведомления</p>
          <div className="flex flex-col gap-3">
            {(
              [
                { key: 'on_trade', label: 'По сделке' },
                { key: 'on_signal', label: 'По сигналу' },
                { key: 'on_balance', label: 'По балансу' },
              ] as { key: keyof NotificationSettings; label: string }[]
            ).map(({ key, label }) => (
              <div key={key} className="flex items-center justify-between">
                <span className="text-sm text-slate-300">{label}</span>
                <button
                  type="button"
                  onClick={() => handleToggle(key)}
                  className={
                    'w-9 h-5 rounded-full transition-colors relative ' +
                    (notifications[key] ? 'bg-[#5b8cff]' : 'bg-white/[.12]')
                  }
                >
                  <span
                    className={
                      'absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ' +
                      (notifications[key] ? 'translate-x-4' : 'translate-x-0.5')
                    }
                  />
                </button>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

// ─── Referrals Tab ────────────────────────────────────────────────────────────

function ReferralsTab({ referral }: { referral: ReferralInfo }) {
  const [copied, setCopied] = useState(false)

  function handleCopy() {
    navigator.clipboard.writeText(referral.link).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }

  return (
    <div className="flex flex-col gap-4 max-w-[480px]">
      {/* Referral link */}
      <div className="bg-white/[.04] border border-white/[.08] rounded-xl p-4">
        <p className="text-[11px] font-medium text-slate-400 uppercase tracking-wide mb-1.5">
          Ваша реферальная ссылка
        </p>
        <div className="flex gap-2">
          <div className="flex-1 bg-white/[.04] border border-white/[.08] rounded-lg px-3.5 py-2.5 text-sm text-slate-300 truncate">
            {referral.link}
          </div>
          <button
            type="button"
            onClick={handleCopy}
            className="px-4 bg-white/[.06] border border-white/[.1] rounded-lg text-sm text-slate-300 hover:bg-white/[.1] transition-colors shrink-0"
          >
            {copied ? '✓' : 'Копировать'}
          </button>
        </div>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-2 gap-3">
        <div className="bg-white/[.04] border border-white/[.08] rounded-xl p-4">
          <p className="text-[11px] font-medium text-slate-500 uppercase tracking-wide mb-1">
            Приглашено
          </p>
          <p className="text-2xl font-bold text-slate-100">{referral.count}</p>
        </div>
        <div className="bg-white/[.04] border border-white/[.08] rounded-xl p-4">
          <p className="text-[11px] font-medium text-slate-500 uppercase tracking-wide mb-1">
            Начислено бонусов
          </p>
          <p className="text-2xl font-bold text-slate-100">${referral.total_rewards}</p>
        </div>
      </div>

      {/* Signups table */}
      <div className="bg-white/[.04] border border-white/[.08] rounded-xl p-4">
        <p className="text-sm font-medium text-slate-100 mb-3">Приглашённые</p>
        {referral.signups.length === 0 ? (
          <div className="text-center py-6 text-sm text-slate-500">Приглашённых пока нет</div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="text-[11px] text-slate-500 uppercase tracking-wide">
                <th className="text-left pb-2 font-medium">Дата</th>
                <th className="text-left pb-2 font-medium">Email</th>
                <th className="text-left pb-2 font-medium">Статус</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-white/[.04]">
              {referral.signups.map((s, i) => (
                <tr key={i}>
                  <td className="py-2 text-slate-400 text-xs">
                    {new Date(s.date).toLocaleDateString('ru-RU')}
                  </td>
                  <td className="py-2 text-slate-300">{s.email_masked}</td>
                  <td className="py-2">
                    <span
                      className={
                        'text-[10px] font-semibold px-1.5 py-0.5 rounded-full ' +
                        (s.active
                          ? 'text-emerald-400 bg-emerald-400/10'
                          : 'text-slate-500 bg-white/[.04]')
                      }
                    >
                      {s.active ? 'Активный' : 'Зарегистрирован'}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
