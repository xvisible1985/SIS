export interface Proxy {
  id: number
  protocol: string
  host: string
  port: number
  username?: string
  weight: number
  is_active: boolean
  health_status: string
  last_checked?: string
  fail_count: number
  total_reqs: number
  active_reqs: number
  created_at?: string
  updated_at?: string
}

export interface ProxyMetrics {
  id: number
  protocol: string
  host: string
  port: number
  weight: number
  is_active: boolean
  health_status: string
  pending: number
  total: number
  failures: number
}

export interface CreateProxyBody {
  protocol: string
  host: string
  port: number
  username?: string
  password?: string
  weight: number
}
