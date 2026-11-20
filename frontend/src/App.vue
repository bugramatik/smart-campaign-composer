<script setup lang="ts">
import { onMounted } from 'vue'
import { campaigns, loadMeta, meta, refreshCampaigns } from './lib/meta'
import { statusLabel } from './lib/labels'

const shortDate = (iso: string) => new Date(iso).toLocaleDateString('tr-TR', { day: 'numeric', month: 'short' })

onMounted(() => {
  loadMeta().catch(() => {})
  refreshCampaigns().catch(() => {})
})
</script>

<template>
  <div class="shell">
    <aside class="side">
      <RouterLink to="/" class="brand">Kampanya Atölyesi</RouterLink>
      <RouterLink to="/" class="new-link">Yeni kampanya</RouterLink>
      <nav aria-label="Kampanyalar">
        <RouterLink v-for="c in campaigns" :key="c.id" :to="`/campaigns/${c.id}`" class="item" :title="c.brief">
          <span :class="['dot', c.status]" :aria-label="statusLabel[c.status]"></span>
          <span class="brief">{{ c.brief }}</span>
          <span class="when small muted num">{{ shortDate(c.created_at) }}</span>
        </RouterLink>
      </nav>
      <p v-if="meta" class="engine small muted" :title="meta.llm.provider === 'demo' ? 'ANTHROPIC_API_KEY yok; içerik kural tabanlı üretiliyor' : meta.llm.model">
        <span :class="['led', meta.llm.provider === 'demo' ? 'demo' : 'on']" aria-hidden="true"></span>
        {{ meta.llm.provider === 'demo' ? 'Demo modu' : 'Claude bağlı' }}
      </p>
    </aside>
    <main class="main">
      <RouterView :key="$route.fullPath.split('?')[0]" />
    </main>
  </div>
</template>

<style scoped>
.shell { display: grid; grid-template-columns: 256px 1fr; min-height: 100vh; }
.side {
  padding: 1.5rem 0.75rem 1rem;
  display: flex; flex-direction: column; gap: 1rem;
  position: sticky; top: 0; height: 100vh; overflow: hidden;
}
.brand { font-weight: 800; font-size: 1.05rem; text-decoration: none; padding: 0 0.6rem; letter-spacing: -0.02em; }
.new-link {
  display: block; text-decoration: none; font-weight: 650; font-size: 0.9rem;
  background: var(--ink); color: #fff; border-radius: var(--r-field); padding: 0.55rem 0.75rem;
}
.new-link:hover { background: #26304d; }
nav { overflow-y: auto; flex: 1; display: flex; flex-direction: column; gap: 1px; margin: 0 -0.25rem; padding: 0 0.25rem; }
.item {
  display: grid; grid-template-columns: 7px 1fr auto; gap: 0.6rem; align-items: center;
  text-decoration: none; padding: 0.5rem 0.6rem; border-radius: var(--r-field); color: var(--slate);
}
.item:hover { background: rgb(255 255 255 / 0.55); color: var(--ink); }
.item.router-link-active { background: var(--sheet); color: var(--ink); }
.brief { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-size: 0.9rem; }
.when { font-size: 0.75rem; }
.dot { width: 7px; height: 7px; border-radius: 50%; background: var(--hair); }
.dot.draft { background: transparent; box-shadow: inset 0 0 0 1.5px var(--slate); }
.dot.sending { background: var(--flight); }
.dot.scheduled { background: var(--warn); }
.dot.completed { background: var(--sent); }
.dot.cancelled { background: var(--fail); }
.engine { display: flex; align-items: center; gap: 0.45rem; padding: 0 0.6rem; }
.led { width: 6px; height: 6px; border-radius: 50%; }
.led.on { background: var(--sent); }
.led.demo { background: var(--warn); }
.main { padding: 2.25rem clamp(1rem, 3vw, 3rem) 4rem; min-width: 0; }

@media (max-width: 860px) {
  .shell { grid-template-columns: 1fr; }
  .side { position: static; height: auto; max-height: 38vh; border-bottom: 1px solid var(--hair); }
}
</style>
