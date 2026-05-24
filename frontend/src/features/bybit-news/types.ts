export interface BybitAnnouncement {
  id: number
  announcement_id: string
  title: string
  description?: string
  type_key?: string
  type_title?: string
  tags?: string[]
  url?: string
  date_ts?: number
  is_new_listing: boolean
  is_delisting: boolean
  symbols?: string[]
  markets?: string[]
  max_leverage?: string
  launch_at?: number
  is_pre_market: boolean
  created_at: string
}
