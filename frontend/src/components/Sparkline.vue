<script setup lang="ts">
import { computed } from 'vue'

// Sent-per-second over the last minute, with the per-channel rate limit drawn
// as a dashed line so the cap is visible when a campaign is saturating it.
const props = defineProps<{ values: number[]; limit: number; window?: number }>()
const W = 240
const H = 48
const top = computed(() => Math.max(props.limit * 1.15, ...props.values))
const x = (i: number) => (i / Math.max(1, (props.window ?? 60) - 1)) * W
const y = (v: number) => H - (v / top.value) * H
const points = computed(() => {
  const off = (props.window ?? 60) - props.values.length
  return props.values.map((v, i) => `${x(i + off).toFixed(1)},${y(v).toFixed(1)}`).join(' ')
})
</script>

<template>
  <svg :viewBox="`0 0 ${W} ${H}`" preserveAspectRatio="none" class="spark" role="img" :aria-label="`Saniyede gönderim, sınır ${limit}`">
    <line x1="0" :x2="W" :y1="y(limit)" :y2="y(limit)" class="cap" />
    <polyline v-if="values.length > 1" :points="points" class="line" />
  </svg>
</template>

<style scoped>
.spark { width: 100%; height: 48px; display: block; overflow: visible; }
.cap { stroke: var(--warn); stroke-width: 1; stroke-dasharray: 4 3; vector-effect: non-scaling-stroke; }
.line { fill: none; stroke: var(--ink); stroke-width: 1.5; vector-effect: non-scaling-stroke; stroke-linejoin: round; }
</style>
