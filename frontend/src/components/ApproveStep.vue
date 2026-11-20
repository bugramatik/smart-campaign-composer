<script setup lang="ts">
import { computed, ref } from 'vue'
import { api, ApiError } from '../lib/api'
import { channelLabel, describe, languageLabel } from '../lib/labels'
import type { Campaign, Catalog, Channel, Issue } from '../lib/types'

const props = defineProps<{ campaign: Campaign; catalog: Catalog }>()
const emit = defineEmits<{ approved: [Campaign]; back: [] }>()

const allChannels: Channel[] = ['sms', 'email']
const aud = computed(() => props.campaign.audience)
const eligible = (ch: Channel) => (ch === 'sms' ? aud.value?.sms_eligible : aud.value?.email_eligible) ?? 0
const suggested = props.campaign.segment_notes?.suggested_channels
const channels = ref<Channel[]>(
  allChannels.filter((ch) => eligible(ch) > 0 && (!suggested?.length || suggested.includes(ch))),
)
const when = ref<'now' | 'later'>('now')
const at = ref(defaultLater())
const issues = ref<Issue[]>([])
const sending = ref(false)

function defaultLater(): string {
  const d = new Date(Date.now() + 60 * 60 * 1000)
  d.setMinutes(0, 0, 0)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const total = computed(() => channels.value.reduce((n, ch) => n + eligible(ch), 0))
const fmt = (n: number) => n.toLocaleString('tr-TR')
const languages = computed(() => (props.campaign.content?.variants ?? []).map((v) => languageLabel[v.language] ?? v.language).join(' ve '))

async function approve() {
  sending.value = true
  issues.value = []
  try {
    const scheduled = when.value === 'later' ? new Date(at.value).toISOString() : null
    emit('approved', await api.approve(props.campaign.id, channels.value, scheduled))
  } catch (e) {
    issues.value = e instanceof ApiError && e.issues.length ? e.issues : [{ path: '', message: (e as Error).message }]
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <section class="step">
    <div class="split">
      <div>
        <h3>Kanallar</h3>
        <div class="channels">
          <label v-for="ch in allChannels" :key="ch" class="channel" :class="{ on: channels.includes(ch), off: eligible(ch) === 0 }">
            <input v-model="channels" type="checkbox" :value="ch" :disabled="eligible(ch) === 0" />
            <span class="name">{{ channelLabel[ch] }}</span>
            <span class="num muted">{{ fmt(eligible(ch)) }} kişi</span>
          </label>
        </div>

        <h3 class="gap">Zaman</h3>
        <div class="when">
          <label class="radio"><input v-model="when" type="radio" value="now" /> Hemen</label>
          <label class="radio"><input v-model="when" type="radio" value="later" /> İleri bir tarihte</label>
          <input v-if="when === 'later'" v-model="at" type="datetime-local" aria-label="Gönderim zamanı" class="dt" />
        </div>
      </div>

      <div class="summary">
        <p class="lead"><strong class="figure hl">{{ fmt(total) }}</strong> mesaj</p>
        <p class="when-line">
          {{ when === 'now' ? 'Onaydan hemen sonra' : new Date(at).toLocaleString('tr-TR', { dateStyle: 'long', timeStyle: 'short' }) }}<template v-if="languages">, {{ languages }}</template>
        </p>
        <ul class="small">
          <li v-for="c in campaign.segment?.conditions ?? []" :key="JSON.stringify(c)">{{ describe(c, catalog) }}</li>
        </ul>
      </div>
    </div>

    <ul v-if="issues.length" class="issues">
      <li v-for="(iss, i) in issues" :key="i" class="issue"><code v-if="iss.path">{{ iss.path }}</code> {{ iss.message }}</li>
    </ul>

    <div class="row foot">
      <button class="link" @click="emit('back')">Mesajlara dön</button>
      <span class="spacer" />
      <button class="primary go" :disabled="sending || channels.length === 0" @click="approve">
        {{ sending ? 'Onaylanıyor…' : when === 'now' ? 'Onayla ve gönder' : 'Onayla ve zamanla' }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.split { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 2.5rem; align-items: start; }
.gap { margin-top: 1.5rem; }
.channels { display: grid; gap: 0.5rem; margin-top: 0.5rem; }
.channel {
  display: grid; grid-template-columns: auto 1fr auto; gap: 0.75rem; align-items: center; margin: 0;
  border: 1px solid var(--hair); border-radius: var(--r-field); padding: 0.75rem 0.9rem; cursor: pointer; font-weight: 500;
}
.channel input { width: auto; }
.channel .name { font-weight: 700; }
.channel input { accent-color: var(--ink); }
.channel.on { border-color: var(--ink); box-shadow: inset 0 0 0 1px var(--ink); }
.channel.off { opacity: 0.5; cursor: not-allowed; }
.when { display: grid; gap: 0.4rem; margin-top: 0.5rem; }
.radio { display: flex; gap: 0.5rem; align-items: center; font-weight: 500; margin: 0; }
.radio input { width: auto; }
.dt { max-width: 16rem; margin-left: 1.5rem; }
.summary { display: grid; gap: 0.6rem; align-content: start; }
.lead { font-size: 1.1rem; color: var(--slate); }
.lead strong { color: var(--ink); margin-right: 0.15rem; }
.when-line { font-weight: 600; }
.summary ul { margin: 0.4rem 0 0; padding: 0.75rem 0 0 1.1rem; border-top: 1px solid var(--hair); color: var(--slate); }
.issues { padding-left: 1rem; margin: 1.25rem 0 0; }
.foot { margin-top: 1.75rem; padding-top: 1.25rem; border-top: 1px solid var(--hair); }
.go { padding: 0.65rem 1.4rem; }
code { font-size: 0.85em; background: var(--wash); padding: 0 0.25rem; border-radius: var(--r-chip); }
@media (max-width: 900px) { .split { grid-template-columns: 1fr; } }
</style>
