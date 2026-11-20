<script setup lang="ts">
import { clone } from '../lib/clone'
import { computed, ref, watch } from 'vue'
import { api, ApiError } from '../lib/api'
import { describe, languageLabel, valueName } from '../lib/labels'
import type { Audience, Campaign, Catalog, Issue, Rule } from '../lib/types'
import RuleEditor from './RuleEditor.vue'

const props = defineProps<{ campaign: Campaign; catalog: Catalog; busy: boolean }>()
const emit = defineEmits<{ saved: [Campaign]; regenerate: []; next: [] }>()

const editing = ref(false)
const draft = ref<Rule>({ match: 'all', conditions: [] })
const preview = ref<Audience | null>(null)
const issues = ref<Issue[]>([])
const saving = ref(false)

const audience = computed(() => (editing.value ? preview.value : null) ?? props.campaign.audience)
const notes = computed(() => props.campaign.segment_notes)
const fmt = (n: number) => n.toLocaleString('tr-TR')
const share = (n: number) => (audience.value?.total ? Math.round((n / audience.value.total) * 100) : 0)

function startEdit() {
  draft.value = clone(props.campaign.segment ?? { match: 'all', conditions: [] })
  preview.value = null
  issues.value = []
  editing.value = true
}

// Live audience count while editing: the backend validates and counts the
// draft rule without saving it.
let timer: ReturnType<typeof setTimeout> | undefined
watch(
  draft,
  (rule) => {
    if (!editing.value) return
    clearTimeout(timer)
    timer = setTimeout(async () => {
      try {
        const r = await api.previewSegment(rule)
        preview.value = r.audience
        issues.value = []
      } catch (e) {
        issues.value = e instanceof ApiError && e.issues.length ? e.issues : [{ path: '', message: (e as Error).message }]
      }
    }, 350)
  },
  { deep: true },
)

async function save() {
  saving.value = true
  try {
    emit('saved', await api.saveSegment(props.campaign.id, draft.value))
    editing.value = false
  } catch (e) {
    issues.value = e instanceof ApiError && e.issues.length ? e.issues : [{ path: '', message: (e as Error).message }]
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="step">
    <div v-if="!campaign.segment && !busy" class="empty">
      <p class="muted">Henüz kitle yok.</p>
      <button class="primary" @click="emit('regenerate')">Kitleyi oluştur</button>
    </div>

    <template v-else-if="campaign.segment">
      <div class="split">
        <div class="rule">
          <template v-if="!editing">
            <p v-if="notes?.summary" class="summary">{{ notes.summary }}</p>
            <h3>Kural <span class="muted small">{{ campaign.segment.match === 'all' ? 'hepsi' : 'en az biri' }}</span></h3>
            <ul class="conds">
              <li v-for="(c, i) in campaign.segment.conditions" :key="i">{{ describe(c, catalog) }}</li>
            </ul>
            <div v-if="notes?.assumptions?.length" class="notes">
              <h3>Varsayımlar</h3>
              <ul><li v-for="a in notes.assumptions" :key="a">{{ a }}</li></ul>
            </div>
            <div v-if="notes?.unsupported?.length" class="notes warn">
              <h3>Kurala çevrilemedi</h3>
              <ul><li v-for="u in notes.unsupported" :key="u">{{ u }}</li></ul>
            </div>
            <div class="row actions">
              <button @click="startEdit">Düzenle</button>
              <button :disabled="busy" @click="emit('regenerate')">Yeniden oluştur</button>
            </div>
          </template>

          <template v-else>
            <RuleEditor v-model="draft" :catalog="catalog" />
            <ul v-if="issues.length" class="issues">
              <li v-for="(iss, i) in issues" :key="i" class="issue"><code v-if="iss.path">{{ iss.path }}</code> {{ iss.message }}</li>
            </ul>
            <div class="row actions">
              <button class="primary" :disabled="saving || issues.length > 0" @click="save">Kaydet</button>
              <button @click="editing = false">Vazgeç</button>
            </div>
          </template>
        </div>

        <aside v-if="audience" class="reach">
          <p class="total"><strong class="figure hl">{{ fmt(audience.total) }}</strong> kişi</p>
          <p v-if="editing && preview" class="small muted">Taslak, kaydedilmedi</p>
          <dl>
            <div>
              <dt>SMS izinli</dt>
              <dd class="num">{{ fmt(audience.sms_eligible) }} <span class="muted">%{{ share(audience.sms_eligible) }}</span></dd>
            </div>
            <div>
              <dt>E-posta izinli</dt>
              <dd class="num">{{ fmt(audience.email_eligible) }} <span class="muted">%{{ share(audience.email_eligible) }}</span></dd>
            </div>
            <div v-for="(n, lang) in audience.by_language" :key="lang">
              <dt>{{ languageLabel[lang] ?? lang }}</dt>
              <dd class="num">{{ fmt(n) }}</dd>
            </div>
          </dl>
        </aside>
      </div>

      <div v-if="audience?.sample?.length" class="sample">
        <h3>Örnek kişiler</h3>
        <table>
          <thead>
            <tr><th>Ad</th><th>Şehir</th><th>Dil</th><th class="r">Sipariş</th><th>SMS</th><th>E-posta</th></tr>
          </thead>
          <tbody>
            <tr v-for="u in audience.sample" :key="u.id">
              <td>{{ u.first_name }} {{ u.last_name }}</td>
              <td>{{ u.city }}</td>
              <td>{{ valueName('language', u.language) }}</td>
              <td class="r num">{{ u.total_orders }}</td>
              <td :class="u.sms_opt_in ? 'yes' : 'no'">{{ u.sms_opt_in ? '✓' : '–' }}</td>
              <td :class="u.email_opt_in ? 'yes' : 'no'">{{ u.email_opt_in ? '✓' : '–' }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="row next">
        <span class="spacer" />
        <button class="primary" :disabled="editing || !audience?.total" @click="emit('next')">Mesajlara geç</button>
      </div>
    </template>
  </section>
</template>

<style scoped>
.empty { display: grid; gap: 0.75rem; justify-items: start; }
.split { display: grid; grid-template-columns: minmax(0, 1fr) 260px; gap: 2.5rem; align-items: start; }
.summary { font-size: 1.1rem; line-height: 1.45; margin-bottom: 1.5rem; max-width: 60ch; }
h3 .small { font-weight: 500; margin-left: 0.3rem; }
.conds { list-style: none; margin: 0.5rem 0 0; padding: 0; display: grid; gap: 0.35rem; }
.conds li { padding: 0.45rem 0.75rem; background: var(--wash); border-radius: var(--r-field); }
.notes { margin-top: 1.25rem; }
.notes ul { margin: 0.3rem 0 0; padding-left: 1.1rem; color: var(--slate); }
.notes.warn h3 { color: var(--warn); }
.actions { margin-top: 1.25rem; }
.reach { padding-top: 0.25rem; }
.total { font-size: 1rem; color: var(--slate); }
.total strong { color: var(--ink); margin-right: 0.15rem; }
dl { margin: 1.25rem 0 0; display: grid; gap: 0.5rem; padding-top: 1rem; border-top: 1px solid var(--hair); }
dl div { display: flex; justify-content: space-between; gap: 1rem; }
dt { color: var(--slate); }
dd { margin: 0; font-weight: 600; }
.issues { padding-left: 1rem; margin: 0; }
.sample { margin-top: 2.25rem; overflow-x: auto; }
table { width: 100%; border-collapse: collapse; margin-top: 0.5rem; font-size: 0.9rem; }
th { text-align: left; font-weight: 600; color: var(--slate); border-bottom: 1px solid var(--hair); padding: 0.4rem 0.5rem; }
td { padding: 0.4rem 0.5rem; border-bottom: 1px solid var(--wash); }
.yes { color: var(--sent); }
.no { color: var(--hair); }
.r { text-align: right; }
.next { margin-top: 1.5rem; }
code { font-size: 0.85em; background: var(--wash); padding: 0 0.25rem; border-radius: var(--r-chip); }
@media (max-width: 900px) { .split { grid-template-columns: 1fr; } }
</style>
