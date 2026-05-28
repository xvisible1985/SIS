import axios from 'axios'

export const apiClient = axios.create({
  baseURL: '',
  headers: { 'Content-Type': 'application/json' },
})

// Single flag: once we detect 401 we stop sending new requests and redirect once.
let isLoggingOut = false

apiClient.interceptors.request.use((config) => {
  // Drop outgoing requests if we're already redirecting to login.
  if (isLoggingOut) {
    return new Promise<never>(() => {}) // never resolves → request is silently cancelled
  }
  const token = localStorage.getItem('token') ?? sessionStorage.getItem('token')
  if (token) {
    config.headers = config.headers ?? {}
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

apiClient.interceptors.response.use(
  res => res,
  err => {
    const isAuthEndpoint = err?.config?.url?.startsWith('/auth/')
    if (err?.response?.status === 401 && !isAuthEndpoint && !isLoggingOut) {
      isLoggingOut = true
      localStorage.removeItem('token')
      sessionStorage.removeItem('token')
      window.location.href = '/login'
      return new Promise<never>(() => {})
    }
    // Propagate server error message if available
    const serverMsg = err?.response?.data?.error ?? err?.response?.data?.message
    if (serverMsg) {
      return Promise.reject(new Error(serverMsg))
    }
    return Promise.reject(err)
  }
)
