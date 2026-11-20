import type { GenerateEvent } from './api'
import type { Issue } from './types'

export interface Step {
  text: string
  tone: 'work' | 'ok' | 'fix' | 'fail'
  issues?: Issue[]
}

const stageName: Record<string, string> = { segment: 'kitle kuralını', content: 'mesajları' }

// describeEvent turns a generation stream event into a line of the progress
// log. Returns null for events that only carry data (content.partial, done).
export function describeEvent(e: GenerateEvent): Step | null {
  const d = e.data ?? {}
  switch (e.event) {
    case 'status':
      if (d.stage === 'audience') return { text: 'Kural doğrulandı, kitle sayılıyor', tone: 'work' }
      if (d.state === 'restarted') return { text: 'Yanıt yarıda kesildi, mesajlar baştan yazılıyor', tone: 'fix' }
      return d.attempt > 1
        ? { text: `Claude ${stageName[d.stage] ?? d.stage} düzeltiyor (deneme ${d.attempt})`, tone: 'fix' }
        : { text: `Claude ${stageName[d.stage] ?? d.stage} yazıyor`, tone: 'work' }
    case 'validation': {
      const n = d.issues?.length ?? 0
      const tail = d.will_retry ? 'Hatalar Claude’a düzeltmesi için geri gönderildi.' : 'Deneme hakkı bitti.'
      return { text: `Doğrulama ${n} sorun buldu. ${tail}`, tone: d.will_retry ? 'fix' : 'fail', issues: d.issues }
    }
    case 'segment':
      return { text: `Kitle hazır: ${(d.campaign?.audience?.total ?? 0).toLocaleString('tr-TR')} kişi`, tone: 'ok' }
    case 'content':
      return d.issues?.length
        ? { text: `Mesajlar taslak olarak kaydedildi; ${d.issues.length} alan düzeltilmeli`, tone: 'fix', issues: d.issues }
        : { text: 'Mesajlar doğrulandı ve kaydedildi', tone: 'ok' }
    case 'error':
      return { text: d.message ?? 'Üretim başarısız oldu', tone: 'fail', issues: Array.isArray(d.details) ? d.details : undefined }
  }
  return null
}
