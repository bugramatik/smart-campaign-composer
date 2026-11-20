<script setup lang="ts">
import type { Step } from '../lib/progress'

defineProps<{ steps: Step[]; active: boolean }>()
</script>

<template>
  <ol class="log" aria-live="polite">
    <li v-for="(s, i) in steps" :key="i" :class="[s.tone, { current: active && i === steps.length - 1 }]">
      <span class="text">{{ s.text }}</span>
      <ul v-if="s.issues?.length" class="issues small">
        <li v-for="(iss, j) in s.issues" :key="j"><code>{{ iss.path }}</code> {{ iss.message }}</li>
      </ul>
    </li>
  </ol>
</template>

<style scoped>
.log { list-style: none; margin: 0; padding: 0; display: grid; gap: 0.35rem; }
.log > li { position: relative; padding-left: 1.2rem; }
.log > li::before {
  content: ''; position: absolute; left: 0; top: 0.5em; width: 8px; height: 8px; border-radius: 50%;
  background: var(--hair);
}
.ok::before { background: var(--sent) !important; }
.fix::before { background: var(--warn) !important; }
.fail::before { background: var(--fail) !important; }
.fail .text { color: var(--fail); }
.current::before { background: var(--flight) !important; animation: pulse 1s ease-in-out infinite alternate; }
.issues { margin: 0.2rem 0 0; padding-left: 1rem; color: var(--slate); }
code { font-size: 0.85em; background: var(--wash); padding: 0 0.25rem; border-radius: var(--r-chip); }
@keyframes pulse { to { opacity: 0.3; } }
</style>
