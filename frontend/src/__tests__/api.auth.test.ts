import { describe, test, expect, vi, beforeEach } from 'vitest'

vi.mock('../api/client', () => ({
  apiClient: { post: vi.fn() },
}))

import { apiClient } from '../api/client'
import { login, register } from '../api/auth'

describe('auth API', () => {
  beforeEach(() => vi.clearAllMocks())

  test('login calls POST /auth/login', async () => {
    vi.mocked(apiClient.post).mockResolvedValue({
      data: { token: 'tok1', user_id: 'uid1' },
    })
    const result = await login('a@b.com', 'pass1234')
    expect(apiClient.post).toHaveBeenCalledWith('/auth/login', {
      email: 'a@b.com',
      password: 'pass1234',
    })
    expect(result).toEqual({ token: 'tok1', user_id: 'uid1' })
  })

  test('register calls POST /auth/register', async () => {
    vi.mocked(apiClient.post).mockResolvedValue({
      data: { token: 'tok2', user_id: 'uid2' },
    })
    const result = await register('b@c.com', 'pass1234')
    expect(apiClient.post).toHaveBeenCalledWith('/auth/register', {
      email: 'b@c.com',
      password: 'pass1234',
    })
    expect(result).toEqual({ token: 'tok2', user_id: 'uid2' })
  })
})
