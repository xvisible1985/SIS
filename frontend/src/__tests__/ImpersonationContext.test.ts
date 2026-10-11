import {
  ADMIN_TOKEN_KEY,
  getAdminToken,
  getImpersonatingAs,
  isImpersonating,
  startImpersonation,
  stopImpersonation,
} from '../contexts/ImpersonationContext'

beforeEach(() => localStorage.clear())

test('startImpersonation stores the admin token and swaps in the target token', () => {
  localStorage.setItem('token', 'admin-tok')
  startImpersonation('user-a-tok', { id: 'a', name: 'User A', email: 'a@test.com' })
  expect(localStorage.getItem('token')).toBe('user-a-tok')
  expect(localStorage.getItem(ADMIN_TOKEN_KEY)).toBe('admin-tok')
  expect(isImpersonating()).toBe(true)
  expect(getImpersonatingAs()).toEqual({ id: 'a', name: 'User A', email: 'a@test.com' })
})

// Regression: switching directly from one impersonated user to another (without
// returning to the admin account first) used to re-run startImpersonation while
// 'token' already held the impersonated user's token, not the admin's — silently
// clobbering the saved admin token with it. "Вернуться в свой аккаунт" would then
// return into the FIRST impersonated user, not the real admin.
test('startImpersonation called again while already impersonating keeps the ORIGINAL admin token', () => {
  localStorage.setItem('token', 'admin-tok')
  startImpersonation('user-a-tok', { id: 'a', name: 'User A', email: 'a@test.com' })
  expect(localStorage.getItem(ADMIN_TOKEN_KEY)).toBe('admin-tok')

  // Switch directly to user B, as AdminUserPickerBar's handleSelectUser does while
  // impersonating — at this point localStorage['token'] is still user A's token.
  startImpersonation('user-b-tok', { id: 'b', name: 'User B', email: 'b@test.com' })

  expect(localStorage.getItem('token')).toBe('user-b-tok')
  expect(localStorage.getItem(ADMIN_TOKEN_KEY)).toBe('admin-tok') // NOT 'user-a-tok'
  expect(getImpersonatingAs()).toEqual({ id: 'b', name: 'User B', email: 'b@test.com' })
})

test('stopImpersonation restores the admin token and clears impersonation state', () => {
  localStorage.setItem('token', 'admin-tok')
  startImpersonation('user-a-tok', { id: 'a', name: 'User A', email: 'a@test.com' })
  stopImpersonation()
  expect(localStorage.getItem('token')).toBe('admin-tok')
  expect(localStorage.getItem(ADMIN_TOKEN_KEY)).toBeNull()
  expect(isImpersonating()).toBe(false)
  expect(getImpersonatingAs()).toBeNull()
})

test('stopImpersonation after a direct A-to-B switch restores the real admin, not user A', () => {
  localStorage.setItem('token', 'admin-tok')
  startImpersonation('user-a-tok', { id: 'a', name: 'User A', email: 'a@test.com' })
  startImpersonation('user-b-tok', { id: 'b', name: 'User B', email: 'b@test.com' })
  stopImpersonation()
  expect(localStorage.getItem('token')).toBe('admin-tok')
  expect(isImpersonating()).toBe(false)
})

test('getAdminToken returns null when not impersonating, and the saved token while impersonating', () => {
  expect(getAdminToken()).toBeNull()
  localStorage.setItem('token', 'admin-tok')
  startImpersonation('user-a-tok', { id: 'a', name: 'User A', email: 'a@test.com' })
  expect(getAdminToken()).toBe('admin-tok')
})
