import axios from 'axios'

export const apiClient = axios.create({
  baseURL: '',
  headers: { 'Content-Type': 'application/json' },
})

apiClient.interceptors.request.use((config) => {
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
    if (err?.response?.status === 401 && !isAuthEndpoint) {
      localStorage.removeItem('token')
      sessionStorage.removeItem('token')
      window.location.href = '/login'
      return new Promise(() => {})
    }
    // Propagate server error message if available
    const serverMsg = err?.response?.data?.error ?? err?.response?.data?.message
    if (serverMsg) {
      return Promise.reject(new Error(serverMsg))
    }
    return Promise.reject(err)
  }
)
