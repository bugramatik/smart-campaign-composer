<script setup lang="ts">
import { clone } from '../lib/clone'
import { computed, nextTick, ref, watch } from 'vue'
import { api, ApiError } from '../lib/api'
import { languageLabel } from '../lib/labels'
import { chars } from '../lib/sms'
import type { Campaign, Content, Issue, Meta, Variant } from '../lib/types'
import MarkedText from './MarkedText.vue'
import SmsRuler from './SmsRuler.vue'

const props = defineProps<{
  campaign: Campaign
  meta: Meta
  busy: boolean
  partial: Content | null
  genIssues: Issue[]
}>()
const emit = defineEmits<{ saved: [Campaign]; regenerate: [feedback: string]; next: [] }>()

type FieldKey = 'sms' | 'email_subject' | 'email_preheader' | 'email_body'

const draft = ref<Content>({ variants: [] })
const dirty = ref(false)
const issues = ref<Issue[]>([])
const checked = ref(false)
const saving = ref(false)
const saveError = ref('')
const lang = ref('')
const feedback = ref('')
const sampleIndex = ref(0)

function reset(c: Content | null) {
  draft.value = clone(c ?? { variants: [] })
  dirty.value = false
  if (!draft.value.variants.some((v) => v.language === lang.value)) lang.value = draft.value.variants[0]?.language ?? ''
}

watch(() => props.campaign.content, (c) => reset(c), { immediate: true })
watch(() => props.genIssues, (g) => { issues.value = g; checked.value = g.length === 0 }, { immediate: true })

// While Claude is writing, the editor shows the stream read-only.
const streaming = computed(() => props.busy && props.partial !== null)
const shown = computed<Content>(() => (streaming.value ? props.partial! : draft.value))
const index = computed(() => Math.max(0, shown.value.variants.findIndex((v) => v.language === lang.value)))
const variant = computed<Variant | undefined>(() => shown.value.variants[index.value])
const maxLengths = computed(() => props.campaign.audience?.max_lengths ?? {})
const limits = computed(() => props.meta.limits)

watch(
  () => props.partial?.variants.map((v) => v.language).join(),
  () => { if (streaming.value && !lang.value) lang.value = props.partial?.variants[0]?.language ?? '' },
)

const samples = computed(() => (props.campaign.audience?.sample ?? []).filter((u) => u.language === variant.value?.language))
const sample = computed(() => {
  const u = samples.value.length ? samples.value[sampleIndex.value % samples.value.length]! : undefined
  return u ? { first_name: u.first_name, last_name: u.last_name, city: u.city } : { first_name: 'Ayşe', last_name: 'Yılmaz', city: 'İstanbul' }
})

function issuesAt(field: string): Issue[] {
  const p = `variants[${index.value}].${field}`
  return issues.value.filter((i) => i.path === p)
}
const otherIssues = computed(() => issues.value.filter((i) => !/^variants\[\d+\]\.(sms|email_subject|email_preheader|email_body)$/.test(i.path)))
const issueLangs = computed(() => {
  const s = new Set<string>()
  for (const i of issues.value) {
    const m = /^variants\[(\d+)\]/.exec(i.path)
    if (m) s.add(draft.value.variants[Number(m[1])]?.language ?? '')
  }
  return s
})

function edit(field: FieldKey, value: string) {
  const v = draft.value.variants[index.value]
  if (!v) return
  v[field] = value
  dirty.value = true
}

// Live validation against the same rules approval uses.
let timer: ReturnType<typeof setTimeout> | undefined
watch(
  draft,
  (c) => {
    if (!dirty.value) return
    checked.value = false
    clearTimeout(timer)
    timer = setTimeout(async () => {
      try {
        const r = await api.checkContent(props.campaign.id, c)
        issues.value = r.issues
        checked.value = true
      } catch {
        checked.value = false
      }
    }, 300)
  },
  { deep: true },
)

// Variable insertion goes into whichever field had focus last.
const focused = ref<{ field: FieldKey; el: HTMLInputElement | HTMLTextAreaElement } | null>(null)
function track(field: FieldKey, e: FocusEvent) {
  focused.value = { field, el: e.target as HTMLInputElement | HTMLTextAreaElement }
}
async function insert(name: string) {
  const f = focused.value ?? { field: 'sms' as FieldKey, el: null }
  const el = f.el
  const cur = variant.value?.[f.field] ?? ''
  const at = el?.selectionStart ?? cur.length
  const end = el?.selectionEnd ?? at
  const token = `{{${name}}}`
  edit(f.field, cur.slice(0, at) + token + cur.slice(end))
  await nextTick()
  el?.focus()
  el?.setSelectionRange(at + token.length, at + token.length)
}

async function save(): Promise<boolean> {
  saving.value = true
  saveError.value = ''
  try {
    const c = await api.saveContent(props.campaign.id, draft.value)
    emit('saved', c)
    dirty.value = false
    return true
  } catch (e) {
    if (e instanceof ApiError && e.issues.length) issues.value = e.issues
    else saveError.value = (e as Error).message
    return false
  } finally {
    saving.value = false
  }
}

async function proceed() {
  if (dirty.value && !(await save())) return
  emit('next')
}

function regenerate() {
  emit('regenerate', feedback.value.trim())
  feedback.value = ''
}

const valid = computed(() => checked.value && issues.value.length === 0)
const counter = (s: string, limit: number) => `${chars(s)} / ${limit}`
</script>

<template>
  <section class="step">
    <div v-if="!shown.variants.length && !busy" class="empty">
      <p class="muted">Henüz mesaj yok.</p>
      <button class="primary" :disabled="!campaign.segment" @click="emit('regenerate', '')">Mesajları yaz</button>
    </div>
    <p v-else-if="!shown.variants.length" class="muted">Claude yazıyor…</p>

    <template v-else>
      <div class="toolbar row">
        <div v-if="shown.variants.length > 1" class="tabs" role="tablist" aria-label="Dil">
          <button
            v-for="v in shown.variants"
            :key="v.language"
            role="tab"
            :aria-selected="v.language === variant?.language"
            :class="{ on: v.language === variant?.language }"
            @click="lang = v.language"
          >
            {{ languageLabel[v.language] ?? v.language }}
            <span v-if="issueLangs.has(v.language)" class="flag" aria-label="düzeltilmesi gereken alan var" />
          </button>
        </div>
        <span class="spacer" />
        <button v-for="name in meta.variables" :key="name" class="varbtn" :disabled="streaming" :title="`${name} ekle`" @mousedown.prevent @click="insert(name)">
          <span class="var" v-text="`{{${name}}}`"></span>
        </button>
      </div>

      <div v-if="variant" class="grid" :class="{ streaming }">
        <div class="fields">
          <div class="field">
            <label :for="`sms-${index}`">SMS</label>
            <textarea
              :id="`sms-${index}`"
              rows="4"
              :value="variant.sms"
              :readonly="streaming"
              @focus="track('sms', $event)"
              @input="edit('sms', ($event.target as HTMLTextAreaElement).value)"
            />
            <SmsRuler :text="variant.sms" :limit="limits.sms" :max-lengths="maxLengths" />
            <p v-for="(iss, i) in issuesAt('sms')" :key="i" class="issue">{{ iss.message }}</p>
          </div>

          <div class="field">
            <div class="labelrow">
              <label :for="`subj-${index}`">E-posta konusu</label>
              <span class="small muted num">{{ counter(variant.email_subject, limits.email_subject) }}</span>
            </div>
            <input
              :id="`subj-${index}`"
              :value="variant.email_subject"
              :readonly="streaming"
              @focus="track('email_subject', $event)"
              @input="edit('email_subject', ($event.target as HTMLInputElement).value)"
            />
            <p v-for="(iss, i) in issuesAt('email_subject')" :key="i" class="issue">{{ iss.message }}</p>
          </div>

          <div class="field">
            <div class="labelrow">
              <label :for="`pre-${index}`">Ön metin</label>
              <span class="small muted num">{{ counter(variant.email_preheader, limits.email_preheader) }}</span>
            </div>
            <input
              :id="`pre-${index}`"
              :value="variant.email_preheader"
              :readonly="streaming"
              @focus="track('email_preheader', $event)"
              @input="edit('email_preheader', ($event.target as HTMLInputElement).value)"
            />
            <p v-for="(iss, i) in issuesAt('email_preheader')" :key="i" class="issue">{{ iss.message }}</p>
          </div>

          <div class="field">
            <div class="labelrow">
              <label :for="`body-${index}`">E-posta metni</label>
              <span class="small muted num">{{ counter(variant.email_body, limits.email_body) }}</span>
            </div>
            <textarea
              :id="`body-${index}`"
              rows="9"
              :value="variant.email_body"
              :readonly="streaming"
              @focus="track('email_body', $event)"
              @input="edit('email_body', ($event.target as HTMLTextAreaElement).value)"
            />
            <p v-for="(iss, i) in issuesAt('email_body')" :key="i" class="issue">{{ iss.message }}</p>
          </div>
        </div>

        <div class="preview" aria-label="Önizleme">
          <div class="row who small">
            <strong>{{ sample.first_name }} {{ sample.last_name }}</strong>
            <span class="muted">{{ sample.city }}</span>
            <span class="spacer" />
            <button v-if="samples.length > 1" class="link small" @click="sampleIndex++">Başka biri</button>
          </div>
          <div class="phone">
            <p class="sender small muted">Mağaza</p>
            <p class="bubble"><MarkedText :text="variant.sms" :values="sample" /></p>
          </div>
          <article class="mail">
            <p class="subject"><MarkedText :text="variant.email_subject" :values="sample" /></p>
            <p class="pre small muted"><MarkedText :text="variant.email_preheader" :values="sample" /></p>
            <div class="body"><MarkedText :text="variant.email_body" :values="sample" /></div>
          </article>
        </div>
      </div>

      <ul v-if="otherIssues.length" class="other">
        <li v-for="(iss, i) in otherIssues" :key="i" class="issue"><code>{{ iss.path }}</code> {{ iss.message }}</li>
      </ul>

      <div class="foot">
        <form class="regen row" @submit.prevent="regenerate">
          <input v-model="feedback" :disabled="busy" placeholder="Claude'a not: daha kısa, emojisiz…" aria-label="Claude'a yeniden yazma notu" maxlength="1000" />
          <button :disabled="busy">Yeniden yaz</button>
        </form>
        <div class="row">
          <span v-if="busy" class="small muted">Claude yazıyor…</span>
          <span v-else-if="dirty && !checked" class="small muted">Kontrol ediliyor…</span>
          <span v-else-if="issues.length" class="issue">{{ issues.length }} alan düzeltilmeli</span>
          <span v-else-if="dirty" class="small muted">Kaydedilmedi</span>
          <span v-if="saveError" class="issue">{{ saveError }}</span>
          <span class="spacer" />
          <button v-if="dirty" class="link" :disabled="busy || saving" @click="reset(campaign.content)">Geri al</button>
          <button v-if="dirty" :disabled="busy || saving || !valid" @click="save">Kaydet</button>
          <button class="primary" :disabled="busy || saving || !valid" @click="proceed">{{ dirty ? 'Kaydet ve onaya geç' : 'Onaya geç' }}</button>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.empty { display: grid; gap: 0.75rem; justify-items: start; }
.toolbar { margin-bottom: 1rem; gap: 0.4rem; }
.tabs { display: flex; gap: 0.25rem; background: var(--wash); padding: 3px; border-radius: var(--r-field); }
.tabs button { border: 0; background: transparent; padding: 0.35rem 0.8rem; font-weight: 600; color: var(--slate); position: relative; }
.tabs button.on { background: var(--sheet); color: var(--ink); box-shadow: 0 1px 2px rgb(26 34 56 / 0.12); }
.flag { display: inline-block; width: 6px; height: 6px; border-radius: 50%; background: var(--fail); margin-left: 0.3rem; vertical-align: middle; }
.varbtn { padding: 0.2rem 0.35rem; font-weight: 500; font-size: 0.85rem; border-color: transparent; background: transparent; }
.varbtn:hover:not(:disabled) { border-color: var(--hair); }
.grid { display: grid; grid-template-columns: minmax(0, 1.1fr) minmax(0, 1fr); gap: 2rem; align-items: start; }
.streaming textarea, .streaming input { background: var(--wash); }
.fields { display: grid; gap: 1.15rem; }
.labelrow { display: flex; justify-content: space-between; align-items: baseline; }
.field .issue { margin-top: 0.25rem; }
.preview { position: sticky; top: 1rem; display: grid; gap: 1rem; }
.who { gap: 0.4rem; }
.phone { background: var(--wash); border-radius: 24px; padding: 1rem 1rem 1.25rem; max-width: 340px; box-shadow: inset 0 0 0 1px var(--hair); }
.sender { text-align: center; margin-bottom: 0.6rem; }
.bubble {
  background: #e4e7ee; border-radius: 18px 18px 18px 4px; padding: 0.6rem 0.85rem;
  max-width: 92%; line-height: 1.4; font-size: 0.95rem;
}
.mail { border: 1px solid var(--hair); border-radius: var(--r-field); padding: 1.1rem 1.2rem; background: var(--sheet); box-shadow: 0 10px 28px -20px rgb(23 32 54 / 0.35); }
.subject { font-weight: 700; font-size: 1.05rem; }
.pre { margin-top: 0.15rem; }
.mail .body { margin-top: 0.9rem; padding-top: 0.9rem; border-top: 1px solid var(--wash); line-height: 1.6; max-width: 62ch; }
.other { padding-left: 1rem; margin: 1rem 0 0; }
.foot { margin-top: 1.75rem; padding-top: 1.25rem; border-top: 1px solid var(--hair); display: grid; gap: 0.9rem; }
.regen { flex-wrap: nowrap; }
.regen input { flex: 1; }
.regen button { white-space: nowrap; }
code { font-size: 0.85em; background: var(--wash); padding: 0 0.25rem; border-radius: var(--r-chip); }
@media (max-width: 1000px) { .grid { grid-template-columns: 1fr; } .preview { position: static; } }
</style>
