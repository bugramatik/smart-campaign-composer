import { SSEParser } from './sse'
import type { Campaign, Content, DeliveryRow, Issue, LLMCall, Meta, Rule, SMSInfo, Snapshot, Audience, Channel } from './types'

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public details?: unknown,
  ) {
    super(message)
  }

  get issues(): Issue[] {
    return Array.isArray(this.details) ? (this.details as Issue[]) : []
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw await toError(res)
  return (await res.json()) as T
}

async function toError(res: Response): Promise<ApiError> {
  try {
    const { error } = await res.json()
    return new ApiError(res.status, error.code, error.message, error.details)
  } catch {
    return new ApiError(res.status, 'http_error', `İstek başarısız oldu (HTTP ${res.status})`)
  }
}

export const api = {
  meta: () => request<Meta>('GET', '/api/meta'),
  list: () => request<{ campaigns: Campaign[] }>('GET', '/api/campaigns?limit=100').then((r) => r.campaigns),
  create: (brief: string) => request<Campaign>('POST', '/api/campaigns', { brief }),
  get: (id: number) => request<Campaign>('GET', `/api/campaigns/${id}`),
  previewSegment: (rule: Rule) => request<{ rule: Rule; audience: Audience }>('POST', '/api/segments/preview', { rule }),
  saveSegment: (id: number, rule: Rule) => request<Campaign>('PUT', `/api/campaigns/${id}/segment`, { rule }),
  saveContent: (id: number, content: Content) => request<Campaign>('PUT', `/api/campaigns/${id}/content`, content),
  checkContent: (id: number, content: Content) =>
    request<{ valid: boolean; issues: Issue[]; sms: Record<string, SMSInfo> }>('POST', `/api/campaigns/${id}/content/check`, content),
  approve: (id: number, channels: Channel[], scheduledAt: string | null) =>
    request<Campaign>('POST', `/api/campaigns/${id}/approve`, { channels, scheduled_at: scheduledAt }),
  cancel: (id: number) => request<Campaign>('POST', `/api/campaigns/${id}/cancel`),
  deliveries: (id: number, status = '') =>
    request<{ deliveries: DeliveryRow[] }>('GET', `/api/campaigns/${id}/deliveries?limit=40${status ? `&status=${status}` : ''}`).then(
      (r) => r.deliveries,
    ),
  llmCalls: (id: number) => request<{ calls: LLMCall[] }>('GET', `/api/campaigns/${id}/llm-calls`).then((r) => r.calls),
}

export type GenerateEvent = { event: string; data: any }

// generate streams the Claude generation for a campaign.
export async function generate(
  id: number,
  body: { stages: 'all' | 'content'; feedback?: string },
  onEvent: (e: GenerateEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const res = await fetch(`/api/campaigns/${id}/generate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok || !res.body) throw await toError(res)
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
  const parser = new SSEParser()
  for (;;) {
    const { value, done } = await reader.read()
    if (done) return
    for (const m of parser.push(value)) onEvent({ event: m.event, data: JSON.parse(m.data) })
  }
}

// watch subscribes to the live campaign snapshot stream.
export function watch(id: number, onSnapshot: (s: Snapshot) => void, onError: (connected: boolean) => void): () => void {
  const es = new EventSource(`/api/campaigns/${id}/events`)
  es.addEventListener('snapshot', (e) => {
    onError(true)
    onSnapshot(JSON.parse((e as MessageEvent).data))
  })
  es.onerror = () => onError(false)
  return () => es.close()
}
