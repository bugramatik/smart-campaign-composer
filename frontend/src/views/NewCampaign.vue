<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, ApiError } from '../lib/api'
import { upsert } from '../lib/meta'

const router = useRouter()
const brief = ref('')
const busy = ref(false)
const error = ref('')

const examples = [
  { title: 'Sepeti terk edenler', brief: 'Son 30 günde sepete ürün ekleyip satın almayan İstanbullu kullanıcılara %15 indirim kodu gönder. Samimi bir dil kullan.' },
  { title: 'Altın üyelere erken erişim', brief: 'Altın ve platin üyelerimize yeni sezon moda koleksiyonunu duyur, 48 saat erken erişim hakkı ver.' },
  { title: 'Geri kazanım', brief: 'Son 90 günde hiç alışveriş yapmayan ama elektronik ürünleri inceleyen kullanıcıları geri kazan; kargo bedava.' },
  { title: 'Hafta sonu spor', brief: 'İzmir ve Ankara’daki 25–40 yaş arası kullanıcılara hafta sonu spor ürünlerinde 2 al 1 öde kampanyası.' },
]

const length = computed(() => [...brief.value.trim()].length)
const ready = computed(() => length.value >= 10 && length.value <= 2000)

async function submit() {
  if (!ready.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    const c = await api.create(brief.value.trim())
    upsert(c)
    await router.push({ name: 'campaign', params: { id: c.id }, query: { generate: '1' } })
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'Kampanya oluşturulamadı. API çalışıyor mu?'
    busy.value = false
  }
}
</script>

<template>
  <section class="new">
    <h1>Kime, ne söylemek istiyorsun?</h1>
    <p class="muted lede">Claude kitleyi ve mesajları hazırlar, göndermeden önce sen düzenlersin.</p>

    <form class="composer" @submit.prevent="submit">
      <label for="brief" class="sr">Kampanya özeti</label>
      <textarea
        id="brief"
        v-model="brief"
        rows="4"
        placeholder="Son bir ayda sepetini terk edenlere ücretsiz kargo hatırlat…"
        @keydown.meta.enter="submit"
        @keydown.ctrl.enter="submit"
      />
      <div class="row foot">
        <div class="examples" aria-label="Örnekler">
          <button v-for="ex in examples" :key="ex.title" type="button" class="chip" :title="ex.brief" @click="brief = ex.brief">
            {{ ex.title }}
          </button>
        </div>
        <span class="spacer" />
        <span v-if="length > 1800" class="small muted num">{{ length }} / 2000</span>
        <button class="primary go" :disabled="!ready || busy">{{ busy ? 'Oluşturuluyor…' : 'Oluştur' }}</button>
      </div>
      <p v-if="error" class="issue err">{{ error }}</p>
    </form>
  </section>
</template>

<style scoped>
.new { max-width: 780px; padding-top: clamp(1rem, 8vh, 5rem); }
h1 { font-size: clamp(2rem, 3.6vw, 2.9rem); line-height: 1.08; letter-spacing: -0.03em; font-weight: 800; max-width: 15ch; }
.lede { margin-top: 0.75rem; }
.composer {
  margin-top: 2rem; background: var(--sheet); border-radius: var(--r-sheet);
  box-shadow: 0 1px 0 var(--hair), 0 12px 32px -18px rgb(23 32 54 / 0.25);
}
.composer:focus-within { box-shadow: 0 0 0 2px var(--ink), 0 12px 32px -18px rgb(23 32 54 / 0.3); }
textarea {
  font-size: 1.25rem; line-height: 1.5; padding: 1.25rem 1.35rem 0.5rem;
  border: 0; border-radius: var(--r-sheet) var(--r-sheet) 0 0; background: transparent; min-height: 8.5rem; resize: none;
}
textarea:focus { box-shadow: none; }
.foot { padding: 0.75rem 0.85rem 0.85rem 1.1rem; flex-wrap: nowrap; align-items: flex-end; }
.examples { display: flex; flex-wrap: wrap; gap: 0.35rem; }
.chip {
  font-weight: 500; font-size: 0.82rem; padding: 0.3rem 0.65rem; border-radius: 999px;
  background: var(--wash); border-color: transparent; color: var(--slate);
}
.chip:hover:not(:disabled) { background: var(--marker-soft); border-color: transparent; color: var(--ink); }
.go { padding: 0.6rem 1.4rem; white-space: nowrap; }
.err { padding: 0 1.1rem 0.9rem; }
.sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
@media (max-width: 600px) { .foot { flex-wrap: wrap; } }
</style>
