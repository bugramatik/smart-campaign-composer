<script setup lang="ts">
import { computed } from 'vue'
import { tokens } from '../lib/sms'

// Renders a template with its {{variables}} marked. With `values`, each
// variable is replaced by the user's value and still highlighted, so the
// marketer sees exactly what was personalized.
const props = defineProps<{ text: string; values?: Record<string, string> }>()
const parts = computed(() => tokens(props.text))
</script>

<template>
  <span class="marked"><template v-for="(p, i) in parts" :key="i"><span
        v-if="p.variable && values"
        class="filled"
        :title="`{{${p.variable}}}`"
        >{{ values[p.variable] ?? '' }}</span
      ><span v-else-if="p.variable" class="var">{{ p.text }}</span
      ><template v-else>{{ p.text }}</template></template></span>
</template>

<style scoped>
.marked { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
