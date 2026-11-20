<script setup lang="ts">
import { computed } from 'vue'
import { analyzeSMS, segmentBreaks, worstCaseLength, worstCaseText } from '../lib/sms'

// A character ruler under the SMS field: the fill is the length with the
// longest audience values substituted (what the backend validates), the ticks
// are where a carrier would split the message into billable parts.
const props = defineProps<{ text: string; limit: number; maxLengths: Record<string, number> }>()

const length = computed(() => worstCaseLength(props.text, props.maxLengths))
const info = computed(() => analyzeSMS(worstCaseText(props.text, props.maxLengths)))
const ticks = computed(() => segmentBreaks(info.value, props.limit))
const over = computed(() => length.value > props.limit)
const fill = computed(() => Math.min(100, (length.value / props.limit) * 100))
</script>

<template>
  <div class="ruler" :class="{ over }">
    <div
      class="track"
      role="meter"
      :aria-valuenow="length"
      aria-valuemin="0"
      :aria-valuemax="limit"
      :aria-label="`SMS uzunluğu ${length} / ${limit}`"
    >
      <div class="fill" :style="{ width: fill + '%' }" />
      <span v-for="t in ticks" :key="t" class="tick" :style="{ left: (t / limit) * 100 + '%' }" />
    </div>
    <div class="legend small">
      <strong class="num" data-test="length">{{ length }} / {{ limit }}</strong>
      <span class="muted">en uzun isimle</span>
      <span class="spacer" />
      <span :class="['enc', info.encoding === 'UCS-2' ? 'ucs' : '']" data-test="encoding">{{ info.encoding }}</span>
      <span class="num" data-test="segments">{{ info.segments }} parça</span>
    </div>
    <p v-if="info.encoding === 'UCS-2' && info.segments > 1" class="small muted hint">
      Türkçe karakterler (ş, ğ, ı) parça sınırını 70'e düşürür.
    </p>
  </div>
</template>

<style scoped>
.ruler { margin-top: 0.4rem; }
.track { position: relative; height: 6px; background: var(--wash); border-radius: 3px; overflow: hidden; border: 1px solid var(--hair); }
.fill { position: absolute; inset: 0 auto 0 0; background: var(--ink); transition: width 120ms ease-out; }
.over .fill { background: var(--fail); }
.tick { position: absolute; top: -1px; bottom: -1px; width: 2px; background: var(--marker); }
.legend { display: flex; gap: 0.5rem; align-items: baseline; margin-top: 0.3rem; }
.over .legend strong { color: var(--fail); }
.enc { padding: 0 0.35rem; border-radius: var(--r-chip); background: var(--wash); }
.enc.ucs { background: var(--marker-soft); }
.hint { margin-top: 0.2rem; }
</style>
