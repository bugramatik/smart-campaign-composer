<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '../lib/api'
import { formatDate } from '../lib/labels'
import type { LLMCall } from '../lib/types'

// The audit trail of every model call for this campaign: what was asked,
// whether the answer passed validation and what it cost.
const props = defineProps<{ campaignId: number; version: number }>()
const open = ref(false)
const calls = ref<LLMCall[]>([])

async function load() {
  calls.value = await api.llmCalls(props.campaignId).catch(() => calls.value)
}
watch(open, (o) => o && load())
watch(() => props.version, () => open.value && load())

const purpose: Record<string, string> = { segment: 'Kitle kuralı', content: 'Mesajlar' }
const outcome: Record<string, string> = { ok: 'Geçerli', invalid: 'Geçersiz', error: 'Hata', truncated: 'Kesildi' }
</script>

<template>
  <details class="log" @toggle="open = ($event.target as HTMLDetailsElement).open">
    <summary>Claude çağrıları</summary>
    <p v-if="calls.length === 0" class="small muted">Henüz çağrı yok</p>
    <table v-else>
      <thead>
        <tr><th>Zaman</th><th>Amaç</th><th class="r">Deneme</th><th>Sonuç</th><th>Model</th><th class="r" title="girdi / çıktı">Token</th><th class="r">Süre</th></tr>
      </thead>
      <tbody>
        <template v-for="c in calls" :key="c.id">
          <tr>
            <td class="num">{{ formatDate(c.created_at) }}</td>
            <td>{{ purpose[c.purpose] ?? c.purpose }}</td>
            <td class="r num">{{ c.attempt }}</td>
            <td :class="c.outcome === 'ok' ? 'ok' : 'bad'">{{ outcome[c.outcome] ?? c.outcome }}</td>
            <td class="small">{{ c.model }} <span class="muted">{{ c.prompt_version }}</span></td>
            <td class="r num">{{ c.input_tokens ?? '–' }} / {{ c.output_tokens ?? '–' }}</td>
            <td class="r num">{{ (c.latency_ms / 1000).toFixed(1) }} sn</td>
          </tr>
          <tr v-if="c.errors?.length" class="errs">
            <td colspan="7">
              <ul class="small"><li v-for="(e, i) in c.errors" :key="i"><code>{{ e.path }}</code> {{ e.message }}</li></ul>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
  </details>
</template>

<style scoped>
.log { margin-top: 2rem; }
summary { cursor: pointer; font-weight: 600; color: var(--slate); font-size: 0.9rem; }
summary:hover { color: var(--ink); }
table { width: 100%; border-collapse: collapse; margin-top: 0.75rem; font-size: 0.88rem; }
th { text-align: left; font-weight: 600; color: var(--slate); border-bottom: 1px solid var(--hair); padding: 0.35rem 0.5rem; }
td { padding: 0.35rem 0.5rem; border-bottom: 1px solid var(--wash); }
.r { text-align: right; }
.ok { color: var(--sent); }
.bad { color: var(--warn); }
.errs td { border-bottom: 1px solid var(--hair); }
.errs ul { margin: 0; padding-left: 1rem; color: var(--slate); }
code { font-size: 0.85em; background: var(--wash); padding: 0 0.25rem; border-radius: var(--r-chip); }
</style>
