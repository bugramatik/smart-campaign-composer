<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { api, watch as watchCampaign } from '../lib/api'
import { channelLabel, deliveryLabel, formatDate } from '../lib/labels'
import { Throughput } from '../lib/throughput'
import type { Campaign, DeliveryRow, Snapshot } from '../lib/types'
import Sparkline from './Sparkline.vue'

const props = defineProps<{ campaign: Campaign }>()
const emit = defineEmits<{ update: [Campaign] }>()

const RATE_LIMIT = 100
const snap = ref<Snapshot | null>(null)
const connected = ref(true)
const rates = reactive<Record<string, number>>({})
const series = reactive<Record<string, number[]>>({})
const tp = new Throughput()
const rows = ref<DeliveryRow[]>([])
const filter = ref('')
const cancelling = ref(false)

const inFlight = ['queued', 'sending', 'retrying']
const order = ['sent', 'failed', 'skipped', 'cancelled'] as const

const channels = computed(() => {
  const stats = snap.value?.stats.channels ?? {}
  return (props.campaign.channels ?? Object.keys(stats)).map((ch) => {
    const s = stats[ch] ?? {}
    const total = Object.values(s).reduce((a, b) => a + b, 0)
    const flight = inFlight.reduce((n, k) => n + (s[k] ?? 0), 0)
    const done = total - flight
    return { ch, s, total, flight, done, pct: total ? Math.round((done / total) * 100) : 0 }
  })
})
const active = computed(() => ['scheduled', 'sending'].includes(props.campaign.status))
const provider = computed(() => snap.value?.provider)
const fmt = (n: number | undefined) => (n ?? 0).toLocaleString('tr-TR')
const width = (n: number, total: number) => (total ? (n / total) * 100 : 0) + '%'

let stop: (() => void) | undefined
let tick = 0
onMounted(() => {
  stop = watchCampaign(
    props.campaign.id,
    (s) => {
      snap.value = s
      const at = Date.parse(s.at)
      for (const [ch, st] of Object.entries(s.stats.channels)) {
        rates[ch] = tp.push(ch, st.sent ?? 0, at)
        series[ch] = [...(tp.series.get(ch) ?? [])]
      }
      if (s.campaign.status !== props.campaign.status || s.campaign.updated_at !== props.campaign.updated_at) emit('update', s.campaign)
      if (tick++ % 3 === 0) loadRows()
    },
    (ok) => (connected.value = ok),
  )
})
onBeforeUnmount(() => stop?.())
watch(filter, () => loadRows())

async function loadRows() {
  try {
    rows.value = await api.deliveries(props.campaign.id, filter.value)
  } catch {
    // The table is secondary; the next snapshot retries.
  }
}

async function cancel() {
  cancelling.value = true
  try {
    emit('update', await api.cancel(props.campaign.id))
  } finally {
    cancelling.value = false
  }
}
</script>

<template>
  <section class="live">
    <div class="row head">
      <p v-if="campaign.status === 'scheduled'" class="state">
        {{ new Date(campaign.scheduled_at!).toLocaleString('tr-TR', { dateStyle: 'long', timeStyle: 'short' }) }} için zamanlandı
      </p>
      <p v-else-if="campaign.status === 'sending'" class="state">Gönderiliyor</p>
      <p v-else-if="campaign.status === 'completed'" class="state muted">{{ formatDate(campaign.completed_at) }} tarihinde bitti</p>
      <p v-else-if="campaign.status === 'cancelled'" class="state">Durduruldu</p>
      <span v-if="!connected" class="small issue">Yeniden bağlanılıyor…</span>
      <span class="spacer" />
      <button v-if="active" class="danger" :disabled="cancelling" @click="cancel">Gönderimi durdur</button>
    </div>

    <div class="channels">
      <div v-for="c in channels" :key="c.ch" class="channel">
        <div class="row top">
          <h2>{{ channelLabel[c.ch] }}</h2>
          <span class="spacer" />
          <span class="big num">{{ fmt(c.s.sent) }}</span>
          <span class="muted num">/ {{ fmt(c.total) }}</span>
        </div>
        <div class="bar" role="img" :aria-label="`${channelLabel[c.ch]}: yüzde ${c.pct} tamamlandı`">
          <span v-for="k in order" :key="k" :class="['seg', k]" :style="{ width: width(c.s[k] ?? 0, c.total) }" />
          <span class="seg flight" :style="{ width: width(c.flight, c.total) }" />
        </div>
        <dl class="counts small">
          <div v-for="k in [...order, ...inFlight]" v-show="(c.s[k] ?? 0) > 0 || k === 'sent'" :key="k">
            <dt><span :class="['key', inFlight.includes(k) ? 'flight' : k]" />{{ deliveryLabel[k] }}</dt>
            <dd class="num">{{ fmt(c.s[k]) }}</dd>
          </div>
        </dl>
        <div v-if="active" class="rate">
          <div class="row small">
            <span class="muted">Mesaj/sn</span>
            <span class="spacer" />
            <strong class="num">{{ Math.round(rates[c.ch] ?? 0) }}</strong>
            <span class="muted">/ {{ RATE_LIMIT }}</span>
          </div>
          <Sparkline :values="series[c.ch] ?? []" :limit="RATE_LIMIT" />
        </div>
        <p v-if="active && snap?.queue[c.ch]" class="small muted num">
          Kuyruk {{ fmt(snap.queue[c.ch]!.pending) }}, tekrar bekleyen {{ fmt(snap.queue[c.ch]!.retry) }}
        </p>
      </div>
    </div>

    <div v-if="snap" class="reliability">
      <p v-if="provider" class="dupes" :class="{ bad: provider.duplicate_deliveries > 0 }">
        <strong class="figure num">{{ fmt(provider.duplicate_deliveries) }}</strong>
        <span>mükerrer teslimat</span>
      </p>
      <dl class="stats">
        <div><dt>Deneme</dt><dd class="num">{{ fmt(snap.stats.attempts) }}</dd></div>
        <div><dt>Yeniden deneme</dt><dd class="num">{{ fmt(snap.stats.retries) }}</dd></div>
      </dl>
      <table v-if="provider" class="provider" aria-label="Sağlayıcı yanıtları, tüm kampanyalar">
        <thead>
          <tr><th>Sağlayıcı</th><th class="r">Kabul</th><th class="r">429</th><th class="r">5xx</th><th class="r">Zaman aşımı</th><th class="r">Tekrar</th></tr>
        </thead>
        <tbody>
          <tr v-for="(p, ch) in provider.channels" :key="ch">
            <td>{{ channelLabel[ch] ?? ch }}</td>
            <td class="r num">{{ fmt(p.accepted) }}</td>
            <td class="r num">{{ fmt(p.rate_limited + p.injected_429) }}</td>
            <td class="r num">{{ fmt(p.server_errors) }}</td>
            <td class="r num">{{ fmt(p.timeouts) }}</td>
            <td class="r num">{{ fmt(p.replayed) }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="table">
      <div class="row">
        <h3>Teslimatlar</h3>
        <span class="spacer" />
        <select v-model="filter" aria-label="Duruma göre filtrele" class="filter">
          <option value="">Tümü</option>
          <option v-for="(label, k) in deliveryLabel" :key="k" :value="k">{{ label }}</option>
        </select>
      </div>
      <table>
        <thead>
          <tr><th>Kişi</th><th>Kanal</th><th>Durum</th><th class="r">Deneme</th><th>Son hata</th><th>Güncellendi</th></tr>
        </thead>
        <tbody>
          <tr v-if="rows.length === 0"><td colspan="6" class="muted">Kayıt yok</td></tr>
          <tr v-for="r in rows" :key="r.id">
            <td>{{ r.first_name }} {{ r.last_name }}</td>
            <td>{{ channelLabel[r.channel] }}</td>
            <td><span :class="['key', inFlight.includes(r.status) ? 'flight' : r.status]" />{{ deliveryLabel[r.status] ?? r.status }}</td>
            <td class="r num">{{ r.attempts }}</td>
            <td class="err small">{{ r.last_error ?? '' }}</td>
            <td class="small muted num">{{ new Date(r.updated_at).toLocaleTimeString('tr-TR') }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<style scoped>
.head { margin-bottom: 1.25rem; }
.state { font-size: 1.1rem; font-weight: 650; }
.channels { display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 2.5rem; }
.top { align-items: baseline; gap: 0.4rem; }
.big { font-size: 2.4rem; font-weight: 800; letter-spacing: -0.03em; line-height: 1; }
.bar { display: flex; height: 14px; border-radius: 7px; overflow: hidden; background: var(--wash); margin: 0.75rem 0; border: 1px solid var(--hair); }
.seg { height: 100%; transition: width 400ms ease-out; }
.sent { background: var(--sent); }
.failed { background: var(--fail); }
.skipped { background: repeating-linear-gradient(135deg, var(--hair) 0 4px, var(--wash) 4px 8px); }
.cancelled { background: var(--slate); }
.flight { background: var(--flight); opacity: 0.45; }
.counts { display: grid; grid-template-columns: repeat(auto-fill, minmax(130px, 1fr)); gap: 0.3rem 1rem; margin: 0; }
.counts div { display: flex; justify-content: space-between; }
dt { color: var(--slate); }
dd { margin: 0; font-weight: 600; }
.key { display: inline-block; width: 9px; height: 9px; border-radius: 2px; margin-right: 0.4rem; vertical-align: baseline; }
.rate { margin: 1rem 0 0.5rem; }
.reliability {
  margin-top: 2.5rem; padding-top: 1.5rem; border-top: 1px solid var(--hair);
  display: grid; grid-template-columns: auto auto 1fr; gap: 2.5rem; align-items: start;
}
.dupes { display: grid; gap: 0.35rem; color: var(--sent); font-weight: 600; }
.dupes.bad { color: var(--fail); }
.stats { margin: 0; display: grid; gap: 0.4rem; min-width: 10rem; }
.stats div { display: flex; justify-content: space-between; gap: 1.5rem; }
.provider { margin-top: 0; }
.table { margin-top: 2.5rem; overflow-x: auto; }
.filter { width: auto; }
table { width: 100%; border-collapse: collapse; margin-top: 0.5rem; font-size: 0.9rem; }
th { text-align: left; font-weight: 600; color: var(--slate); border-bottom: 1px solid var(--hair); padding: 0.35rem 0.5rem; }
td { padding: 0.35rem 0.5rem; border-bottom: 1px solid var(--wash); }
.r { text-align: right; }
.err { color: var(--fail); max-width: 22rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
@media (max-width: 900px) { .reliability { grid-template-columns: 1fr; gap: 1.5rem; } .provider { display: block; overflow-x: auto; } }
</style>
