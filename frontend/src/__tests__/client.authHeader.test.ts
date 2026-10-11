import { apiClient } from '../api/client'

type TestConfig = { headers?: { Authorization?: string } }

// The request interceptor isn't exported directly — grab the registered
// handler off the axios instance the same way the request pipeline invokes it.
function runRequestInterceptor(config: TestConfig): TestConfig {
  const handlers = (apiClient.interceptors.request as unknown as {
    handlers: Array<{ fulfilled: (c: TestConfig) => TestConfig }>
  }).handlers
  return handlers[0].fulfilled(config)
}

beforeEach(() => {
  localStorage.clear()
  sessionStorage.clear()
})

test('attaches the stored token when the caller sets no Authorization header', () => {
  localStorage.setItem('token', 'stored-tok')
  const config = runRequestInterceptor({ headers: {} })
  expect(config.headers?.Authorization).toBe('Bearer stored-tok')
})

// Regression: switching directly between impersonated accounts (AdminUserPickerBar)
// must authenticate the /admin/impersonate/{id} call as the real admin, not
// whichever user is currently impersonated — passed explicitly as a per-call
// header. The interceptor used to unconditionally overwrite it with the stored
// (impersonated) token, which would make the switch request get rejected by the
// backend's RequireAdmin check.
test('does not overwrite an explicitly set Authorization header', () => {
  localStorage.setItem('token', 'impersonated-user-tok')
  const config = runRequestInterceptor({ headers: { Authorization: 'Bearer real-admin-tok' } })
  expect(config.headers?.Authorization).toBe('Bearer real-admin-tok')
})

test('still attaches the stored token when headers is undefined', () => {
  localStorage.setItem('token', 'stored-tok')
  const config = runRequestInterceptor({})
  expect(config.headers?.Authorization).toBe('Bearer stored-tok')
})
