import type { Catalog, Condition, CampaignStatus } from './types'

export const statusLabel: Record<CampaignStatus, string> = {
  draft: 'Taslak',
  scheduled: 'Zamanlandı',
  sending: 'Gönderiliyor',
  completed: 'Tamamlandı',
  cancelled: 'İptal edildi',
}

export const deliveryLabel: Record<string, string> = {
  queued: 'Sırada',
  sending: 'Gönderiliyor',
  retrying: 'Yeniden denenecek',
  sent: 'Gönderildi',
  failed: 'Başarısız',
  skipped: 'Atlandı',
  cancelled: 'İptal',
}

export const channelLabel: Record<string, string> = { sms: 'SMS', email: 'E-posta' }
export const languageLabel: Record<string, string> = { tr: 'Türkçe', en: 'İngilizce' }

const valueLabel: Record<string, Record<string, string>> = {
  language: languageLabel,
  loyalty_tier: { bronze: 'Bronz', silver: 'Gümüş', gold: 'Altın', platinum: 'Platin' },
}

export const eventLabel: Record<string, [string, string]> = {
  page_view: ['siteyi ziyaret etti', 'siteyi ziyaret etmedi'],
  product_view: ['ürün inceledi', 'ürün incelemedi'],
  cart_add: ['sepete ürün ekledi', 'sepete ürün eklemedi'],
  purchase: ['satın aldı', 'satın almadı'],
}

export const categoryLabel: Record<string, string> = {
  electronics: 'Elektronik',
  fashion: 'Moda',
  home: 'Ev',
  beauty: 'Kozmetik',
  sports: 'Spor',
}

export const operatorLabel: Record<string, string> = {
  in: 'şunlardan biri',
  not_in: 'şunlar dışında',
  eq: '=',
  gt: '>',
  gte: '≥',
  lt: '<',
  lte: '≤',
  between: 'arasında',
}

export function valueName(field: string | undefined, v: string): string {
  return valueLabel[field ?? '']?.[v] ?? v
}

const num = (n: number | undefined) => (n ?? 0).toLocaleString('tr-TR')

// describe renders a condition as a short Turkish sentence.
export function describe(c: Condition, cat?: Catalog): string {
  const label = cat?.fields.find((f) => f.name === c.field)?.label ?? c.field ?? ''
  switch (c.kind) {
    case 'text': {
      const vals = (c.values ?? []).map((v) => valueName(c.field, v)).join(', ')
      return c.operator === 'not_in' ? `${label}: ${vals} dışında` : `${label}: ${vals}`
    }
    case 'number':
      return c.operator === 'between'
        ? `${label} ${num(c.value)} ile ${num(c.value_to)} arasında`
        : `${label} ${operatorLabel[c.operator ?? ''] ?? c.operator} ${num(c.value)}`
    case 'event': {
      const [yes, no] = eventLabel[c.event ?? ''] ?? [c.event ?? '', `${c.event} yok`]
      const parts = [
        c.within_days ? `Son ${c.within_days} günde` : 'Herhangi bir zamanda',
        c.category ? `${categoryLabel[c.category] ?? c.category} kategorisinde` : '',
        c.occurred !== false && c.min_count && c.min_count > 1 ? `en az ${c.min_count} kez` : '',
        c.occurred === false ? no : yes,
      ]
      return parts.filter(Boolean).join(' ')
    }
  }
}

export function formatDate(iso: string | null | undefined): string {
  if (!iso) return ''
  return new Date(iso).toLocaleString('tr-TR', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' })
}
