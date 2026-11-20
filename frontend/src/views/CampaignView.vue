<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, ApiError, generate, type GenerateEvent } from '../lib/api'
import { formatDate, statusLabel } from '../lib/labels'
import { loadMeta, meta, upsert } from '../lib/meta'
import { describeEvent, type Step } from '../lib/progress'
import type { Campaign, Content, Issue } from '../lib/types'
import ApproveStep from '../components/ApproveStep.vue'
import AudienceStep from '../components/AudienceStep.vue'
import ContentStep from '../components/ContentStep.vue'
import GenerationLog from '../components/GenerationLog.vue'
import LiveDashboard from '../components/LiveDashboard.vue'
import LlmLog from '../components/LlmLog.vue'

const props = defineProps<{ id: number }>()
const route = useRoute()
const router = useRouter()

type StepKey = 'audience' | 'content' | 'approve'
const campaign = ref<Campaign | null>(null)
const loadError = ref('')
const step = ref<StepKey>('audience')
const busy = ref(false)
const log = ref<Step[]>([])
const partial = ref<Content | null>(null)
const genIssues = ref<Issue[]>([])
const version = ref(0)
let abort: AbortController | null = null

const steps: { key: StepKey; label: string }[] = [
  { key: 'audience', label: 'Kitle' },
  { key: 'content', label: 'Mesajlar' },
  { key: 'approve', label: 'Onay' },
]
const live = computed(() => campaign.value && campaign.value.status !== 'draft')
const reachable = (k: StepKey) =>
  k === 'audience' || (k === 'content' && !!campaign.value?.segment) || (k === 'approve' && !!campaign.value?.content)

function set(c: Campaign) {
  campaign.value = c
  upsert(c)
}

onMounted(async () => {
  try {
    const [c] = await Promise.all([api.get(props.id), loadMeta()])
    set(c)
    step.value = c.content ? 'content' : 'audience'
    if (route.query.generate && !c.segment && c.status === 'draft') {
      router.replace({ query: {} })
      run('all', '')
    }
  } catch (e) {
    loadError.value = e instanceof ApiError && e.status === 404 ? 'Bu kampanya bulunamadı.' : 'Kampanya yüklenemedi. API çalışıyor mu?'
  }
})
onBeforeUnmount(() => abort?.abort())

async function run(stages: 'all' | 'content', feedback: string) {
  if (!campaign.value || busy.value) return
  busy.value = true
  log.value = []
  partial.value = null
  genIssues.value = []
  step.value = stages === 'all' ? 'audience' : 'content'
  abort = new AbortController()
  const onEvent = (e: GenerateEvent) => {
    const line = describeEvent(e)
    if (line) log.value.push(line)
    switch (e.event) {
      case 'status':
        if (e.data.stage === 'content') step.value = 'content'
        break
      case 'content.partial':
        partial.value = e.data.content
        break
      case 'segment':
        set(e.data.campaign)
        break
      case 'content':
        set(e.data.campaign)
        genIssues.value = e.data.issues ?? []
        break
      case 'done':
        set(e.data.campaign)
        break
    }
  }
  try {
    await generate(campaign.value.id, { stages, feedback: feedback || undefined }, onEvent, abort.signal)
  } catch (e) {
    if ((e as Error).name !== 'AbortError') {
      log.value.push({ text: e instanceof ApiError ? e.message : 'Bağlantı kesildi, üretim tamamlanamadı', tone: 'fail', issues: e instanceof ApiError ? e.issues : undefined })
    }
  } finally {
    busy.value = false
    partial.value = null
    version.value++
  }
}
</script>

<template>
  <p v-if="loadError" class="issue">{{ loadError }}</p>
  <div v-else-if="campaign && meta" class="campaign">
    <header>
      <p class="small meta">
        <span :class="['status', campaign.status]">{{ statusLabel[campaign.status] }}</span>
        <span class="muted">{{ formatDate(campaign.created_at) }}</span>
      </p>
      <h1 class="brief">{{ campaign.brief }}</h1>
    </header>

    <nav v-if="!live" class="steps" aria-label="Adımlar">
      <ol>
        <li v-for="(s, i) in steps" :key="s.key">
          <button
            :class="{ on: step === s.key }"
            :aria-current="step === s.key ? 'step' : undefined"
            :disabled="!reachable(s.key) || busy"
            @click="step = s.key"
          >
            <span class="n num">{{ i + 1 }}</span><span :class="{ hl: step === s.key }">{{ s.label }}</span>
          </button>
        </li>
      </ol>
    </nav>

    <div v-if="log.length" class="progress" :class="{ settled: !busy }">
      <GenerationLog :steps="log" :active="busy" />
    </div>

    <div class="sheet">
      <LiveDashboard v-if="live" :campaign="campaign" @update="set" />
      <AudienceStep
        v-else-if="step === 'audience'"
        :campaign="campaign"
        :catalog="meta.catalog"
        :busy="busy"
        @saved="set"
        @regenerate="run('all', '')"
        @next="step = 'content'"
      />
      <ContentStep
        v-else-if="step === 'content'"
        :campaign="campaign"
        :meta="meta"
        :busy="busy"
        :partial="partial"
        :gen-issues="genIssues"
        @saved="set"
        @regenerate="(f: string) => run('content', f)"
        @next="step = 'approve'"
      />
      <ApproveStep v-else :campaign="campaign" :catalog="meta.catalog" @approved="set" @back="step = 'content'" />
    </div>

    <LlmLog :campaign-id="campaign.id" :version="version" />
  </div>
  <p v-else class="muted">Yükleniyor…</p>
</template>

<style scoped>
.campaign { max-width: 1180px; }
header { margin-bottom: 1.75rem; }
.meta { display: flex; gap: 0.6rem; align-items: center; }
.status { font-weight: 650; padding: 0.1rem 0.55rem; border-radius: 999px; background: var(--sheet); }
.status.sending { color: var(--flight); }
.status.completed { color: var(--sent); }
.status.cancelled { color: var(--fail); }
.status.scheduled { color: var(--warn); }
.brief {
  margin-top: 0.6rem; font-size: clamp(1.3rem, 2.3vw, 1.75rem); line-height: 1.3; font-weight: 700;
  letter-spacing: -0.02em; max-width: 50ch;
}
.steps ol { list-style: none; display: flex; gap: 0.25rem; padding: 0; margin: 0 0 0.9rem; }
.steps li { display: flex; align-items: center; }
.steps li + li::before { content: ''; width: 1.25rem; height: 1px; background: var(--hair); margin-right: 0.25rem; }
.steps button {
  border: 0; background: transparent; padding: 0.4rem 0.6rem 0.4rem 0.3rem;
  display: flex; align-items: center; gap: 0.5rem; color: var(--slate); font-weight: 600;
}
.steps button.on { color: var(--ink); }
.steps button:disabled { opacity: 0.4; }
.n {
  width: 1.45rem; height: 1.45rem; border-radius: 50%; display: inline-grid; place-items: center; font-size: 0.75rem;
  box-shadow: inset 0 0 0 1.5px currentColor;
}
.on .n { background: var(--ink); color: #fff; box-shadow: none; }
.progress { margin-bottom: 0.9rem; padding: 0.8rem 1rem; background: var(--sheet); border-radius: var(--r-field); box-shadow: inset 3px 0 0 var(--flight); font-size: 0.9rem; }
.progress.settled { box-shadow: inset 3px 0 0 var(--hair); }
.sheet { background: var(--sheet); border-radius: var(--r-sheet); padding: clamp(1.1rem, 2.5vw, 2rem); box-shadow: 0 1px 0 var(--hair); }
</style>
