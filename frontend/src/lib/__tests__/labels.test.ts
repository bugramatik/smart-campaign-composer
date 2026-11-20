import { describe, expect, it } from 'vitest'
import { describe as describeCond } from '../labels'
import { describeEvent } from '../progress'
import { Throughput } from '../throughput'

describe('condition descriptions', () => {
  it('describes behaviour in Turkish', () => {
    expect(describeCond({ kind: 'event', event: 'cart_add', occurred: true, within_days: 30 })).toBe('Son 30 günde sepete ürün ekledi')
    expect(describeCond({ kind: 'event', event: 'purchase', occurred: false, within_days: 30, category: 'fashion' })).toBe(
      'Son 30 günde Moda kategorisinde satın almadı',
    )
    expect(describeCond({ kind: 'event', event: 'purchase', min_count: 3 })).toBe('Herhangi bir zamanda en az 3 kez satın aldı')
  })

  it('names closed-set values', () => {
    expect(describeCond({ kind: 'text', field: 'loyalty_tier', operator: 'in', values: ['gold', 'platinum'] })).toBe('loyalty_tier: Altın, Platin')
    expect(describeCond({ kind: 'number', field: 'age', operator: 'between', value: 25, value_to: 40 })).toBe('age 25 ile 40 arasında')
  })
})

describe('generation progress', () => {
  it('explains the repair loop', () => {
    const s = describeEvent({ event: 'validation', data: { stage: 'segment', attempt: 1, issues: [{ path: 'rule', message: 'x' }], will_retry: true } })
    expect(s?.tone).toBe('fix')
    expect(s?.text).toContain('geri gönderildi')
    expect(describeEvent({ event: 'status', data: { stage: 'segment', state: 'generating', attempt: 2 } })?.text).toContain('deneme 2')
    expect(describeEvent({ event: 'content.partial', data: {} })).toBeNull()
  })
})

describe('Throughput', () => {
  it('derives a per-second rate from cumulative counts', () => {
    const t = new Throughput(3)
    expect(t.push('sms', 0, 0)).toBe(0)
    expect(t.push('sms', 100, 1000)).toBe(100)
    expect(t.push('sms', 150, 2000)).toBe(50)
    t.push('sms', 150, 3000)
    t.push('sms', 160, 4000)
    expect(t.series.get('sms')).toEqual([50, 0, 10])
  })
})

describe('clone', () => {
  it('copies reactive data that structuredClone rejects', async () => {
    const { reactive } = await import('vue')
    const { clone } = await import('../clone')
    const r = reactive({ variants: [{ language: 'tr', sms: 'x' }] })
    const c = clone(r)
    c.variants[0]!.sms = 'y'
    expect(r.variants[0]!.sms).toBe('x')
  })
})
