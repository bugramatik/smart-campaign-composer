import type { SMSInfo } from './types'

// GSM 03.38 basic set plus extension table; mirrors the backend.
const GSM7 = new Set(
  '@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !"#¤%&\'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà^{}\\[~]|€',
)

export const PLACEHOLDER = /\{\{\s*([a-z_]+)\s*\}\}/g

export function chars(s: string): number {
  return [...s].length
}

export function analyzeSMS(text: string): SMSInfo {
  const n = chars(text)
  const ucs2 = [...text].some((c) => !GSM7.has(c))
  const [single, multi] = ucs2 ? [70, 67] : [160, 153]
  return { characters: n, encoding: ucs2 ? 'UCS-2' : 'GSM-7', segments: n > single ? Math.ceil(n / multi) : 1 }
}

// worstCaseLength is the length with every variable replaced by the longest
// value in the audience, which is what the backend validates.
export function worstCaseLength(tmpl: string, maxLengths: Record<string, number>): number {
  let n = chars(tmpl.replace(PLACEHOLDER, ''))
  for (const m of tmpl.matchAll(PLACEHOLDER)) n += maxLengths[m[1]!] ?? 0
  return n
}

export function render(tmpl: string, vars: Record<string, string>): string {
  return tmpl.replace(PLACEHOLDER, (_, name: string) => vars[name] ?? '')
}

// segmentBreaks are the character positions where a new SMS part starts.
export function segmentBreaks(info: SMSInfo, limit: number): number[] {
  const single = info.encoding === 'UCS-2' ? 70 : 160
  const multi = info.encoding === 'UCS-2' ? 67 : 153
  if (limit <= single) return []
  const out: number[] = []
  for (let p = multi; p < limit; p += multi) out.push(p)
  return out
}

export type Token = { text: string; variable?: string }

// tokens splits a template into literal text and {{variable}} parts so the
// UI can mark placeholders without using v-html.
export function tokens(tmpl: string): Token[] {
  const out: Token[] = []
  let last = 0
  for (const m of tmpl.matchAll(PLACEHOLDER)) {
    if (m.index! > last) out.push({ text: tmpl.slice(last, m.index) })
    out.push({ text: m[0], variable: m[1] })
    last = m.index! + m[0].length
  }
  if (last < tmpl.length) out.push({ text: tmpl.slice(last) })
  return out
}

// worstCaseText substitutes each variable with a string of its longest
// audience length, for encoding and segment analysis.
export function worstCaseText(tmpl: string, maxLengths: Record<string, number>, sample: Record<string, string> = {}): string {
  return tmpl.replace(PLACEHOLDER, (_, name: string) => {
    const n = maxLengths[name] ?? 0
    const base = sample[name] || 'x'
    return base.repeat(Math.ceil(n / Math.max(1, chars(base)))).slice(0, n)
  })
}
