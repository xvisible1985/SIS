// Thin helpers for the localStorage-based impersonation state.
// The actual auth token swap + reload is done in AdminUserPickerBar.

export const ADMIN_TOKEN_KEY = 'admin_token'
export const IMPERSONATING_AS_KEY = 'impersonating_as'

export type ImpersonatingAs = { id: string; name: string; email: string }

export function getImpersonatingAs(): ImpersonatingAs | null {
  try {
    const raw = localStorage.getItem(IMPERSONATING_AS_KEY)
    return raw ? (JSON.parse(raw) as ImpersonatingAs) : null
  } catch {
    return null
  }
}

export function isImpersonating(): boolean {
  return !!localStorage.getItem(ADMIN_TOKEN_KEY)
}

export function startImpersonation(token: string, as: ImpersonatingAs) {
  const current = localStorage.getItem('token') ?? sessionStorage.getItem('token') ?? ''
  localStorage.setItem(ADMIN_TOKEN_KEY, current)
  localStorage.setItem(IMPERSONATING_AS_KEY, JSON.stringify(as))
  localStorage.setItem('token', token)
}

export function stopImpersonation() {
  const adminToken = localStorage.getItem(ADMIN_TOKEN_KEY) ?? ''
  localStorage.setItem('token', adminToken)
  localStorage.removeItem(ADMIN_TOKEN_KEY)
  localStorage.removeItem(IMPERSONATING_AS_KEY)
}
