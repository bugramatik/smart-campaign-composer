export type Channel = 'sms' | 'email'
export type CampaignStatus = 'draft' | 'scheduled' | 'sending' | 'completed' | 'cancelled'

export interface Condition {
  kind: 'text' | 'number' | 'event'
  field?: string
  operator?: string
  values?: string[]
  value?: number
  value_to?: number
  event?: string
  occurred?: boolean
  within_days?: number
  min_count?: number
  category?: string
}

export interface Rule {
  match: 'all' | 'any'
  conditions: Condition[]
}

export interface SampleUser {
  id: number
  first_name: string
  last_name: string
  city: string
  language: string
  sms_opt_in: boolean
  email_opt_in: boolean
  total_orders: number
}

export interface Audience {
  total: number
  sms_eligible: number
  email_eligible: number
  by_language: Record<string, number>
  max_lengths: Record<string, number>
  sample: SampleUser[]
}

export interface Variant {
  language: string
  sms: string
  email_subject: string
  email_preheader: string
  email_body: string
}

export interface Content {
  variants: Variant[]
}

export interface Notes {
  summary: string
  assumptions: string[]
  unsupported: string[]
  suggested_channels: Channel[]
}

export interface Campaign {
  id: number
  brief: string
  status: CampaignStatus
  segment: Rule | null
  segment_notes: Notes | null
  audience: Audience | null
  content: Content | null
  channels: Channel[] | null
  scheduled_at: string | null
  approved_at: string | null
  started_at: string | null
  completed_at: string | null
  created_at: string
  updated_at: string
}

export interface Issue {
  path: string
  message: string
}

export interface Field {
  name: string
  label: string
  type: 'text' | 'number'
  values?: string[]
  min?: number
  max?: number
}

export interface Catalog {
  fields: Field[]
  events: string[]
  categories: string[]
  max_days: number
}

export interface Meta {
  llm: { provider: string; model: string }
  catalog: Catalog
  channels: Channel[]
  operators: { text: string[]; number: string[] }
  variables: string[]
  limits: { sms: number; email_subject: number; email_preheader: number; email_body: number }
}

export interface SMSInfo {
  characters: number
  encoding: 'GSM-7' | 'UCS-2'
  segments: number
}

export interface DeliveryStats {
  channels: Record<string, Record<string, number>>
  attempts: number
  retries: number
}

export interface ProviderChannelStats {
  accepted: number
  replayed: number
  rate_limited: number
  injected_429: number
  server_errors: number
  timeouts: number
  bad_requests: number
}

export interface ProviderStats {
  channels: Record<string, ProviderChannelStats>
  duplicate_deliveries: number
  uptime_seconds: number
}

export interface Snapshot {
  campaign: Campaign
  stats: DeliveryStats
  queue: Record<string, { pending: number; retry: number }>
  provider?: ProviderStats
  at: string
}

export interface DeliveryRow {
  id: number
  user_id: number
  first_name: string
  last_name: string
  channel: Channel
  status: string
  attempts: number
  last_error: string | null
  provider_message_id: string | null
  updated_at: string
  sent_at: string | null
}

export interface LLMCall {
  id: number
  purpose: string
  attempt: number
  provider: string
  model: string
  prompt_version: string
  outcome: string
  errors: Issue[] | null
  stop_reason: string | null
  input_tokens: number | null
  output_tokens: number | null
  latency_ms: number
  created_at: string
}
