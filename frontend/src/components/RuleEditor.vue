<script setup lang="ts">
import { computed } from 'vue'
import type { Catalog, Condition, Rule } from '../lib/types'
import { categoryLabel, eventLabel, operatorLabel, valueName } from '../lib/labels'

// Edits a segment rule against the catalog whitelist. The backend validates
// again; this only keeps the form from offering impossible combinations.
const props = defineProps<{ catalog: Catalog }>()
const rule = defineModel<Rule>({ required: true })

const textFields = computed(() => props.catalog.fields.filter((f) => f.type === 'text'))
const numberFields = computed(() => props.catalog.fields.filter((f) => f.type === 'number'))

function fieldOf(c: Condition) {
  return props.catalog.fields.find((f) => f.name === c.field)
}

function update(i: number, patch: Partial<Condition>) {
  const conditions = rule.value.conditions.slice()
  conditions[i] = { ...conditions[i]!, ...patch }
  rule.value = { ...rule.value, conditions }
}

function setSubject(i: number, subject: string) {
  if (subject === 'event') {
    update(i, { kind: 'event', field: undefined, operator: undefined, values: undefined, value: undefined, value_to: undefined, event: 'purchase', occurred: true, within_days: 30 })
    return
  }
  const f = props.catalog.fields.find((x) => x.name === subject)!
  const conditions = rule.value.conditions.slice()
  conditions[i] =
    f.type === 'text'
      ? { kind: 'text', field: f.name, operator: 'in', values: [] }
      : { kind: 'number', field: f.name, operator: 'gte', value: f.min ?? 0 }
  rule.value = { ...rule.value, conditions }
}

function toggleValue(i: number, v: string) {
  const cur = rule.value.conditions[i]!.values ?? []
  update(i, { values: cur.includes(v) ? cur.filter((x) => x !== v) : [...cur, v] })
}

function setMatch(match: string) {
  rule.value = { ...rule.value, match: match as Rule['match'] }
}

function add() {
  rule.value = { ...rule.value, conditions: [...rule.value.conditions, { kind: 'event', event: 'purchase', occurred: true, within_days: 30 }] }
}

function remove(i: number) {
  rule.value = { ...rule.value, conditions: rule.value.conditions.filter((_, j) => j !== i) }
}

function optionalNumber(v: string): number | undefined {
  return v === '' ? undefined : Number(v)
}
</script>

<template>
  <div class="editor">
    <div class="row match">
      <span>Kullanıcı</span>
      <select :value="rule.match" aria-label="Eşleşme" @change="setMatch(($event.target as HTMLSelectElement).value)">
        <option value="all">aşağıdaki koşulların hepsini</option>
        <option value="any">aşağıdaki koşullardan en az birini</option>
      </select>
      <span>sağlasın.</span>
    </div>

    <ol class="conds">
      <li v-for="(c, i) in rule.conditions" :key="i" class="cond">
        <div class="row">
          <select :value="c.kind === 'event' ? 'event' : c.field" aria-label="Koşul türü" @change="setSubject(i, ($event.target as HTMLSelectElement).value)">
            <optgroup label="Davranış">
              <option value="event">Bir eylem yaptı / yapmadı</option>
            </optgroup>
            <optgroup label="Profil">
              <option v-for="f in textFields" :key="f.name" :value="f.name">{{ f.label }}</option>
            </optgroup>
            <optgroup label="Sayısal">
              <option v-for="f in numberFields" :key="f.name" :value="f.name">{{ f.label }}</option>
            </optgroup>
          </select>
          <span class="spacer" />
          <button type="button" class="link" @click="remove(i)">Kaldır</button>
        </div>

        <div v-if="c.kind === 'text'" class="body">
          <select :value="c.operator" aria-label="Operatör" class="narrow" @change="update(i, { operator: ($event.target as HTMLSelectElement).value })">
            <option value="in">{{ operatorLabel.in }}</option>
            <option value="not_in">{{ operatorLabel.not_in }}</option>
          </select>
          <div class="chips">
            <label v-for="v in fieldOf(c)?.values ?? []" :key="v" class="chip" :class="{ on: c.values?.includes(v) }">
              <input type="checkbox" :checked="c.values?.includes(v)" @change="toggleValue(i, v)" />
              {{ valueName(c.field, v) }}
            </label>
          </div>
        </div>

        <div v-else-if="c.kind === 'number'" class="body row">
          <select :value="c.operator" aria-label="Operatör" class="narrow" @change="update(i, { operator: ($event.target as HTMLSelectElement).value })">
            <option v-for="op in ['gte', 'gt', 'eq', 'lt', 'lte', 'between']" :key="op" :value="op">{{ operatorLabel[op] }}</option>
          </select>
          <input type="number" class="narrow" :value="c.value" aria-label="Değer" @input="update(i, { value: Number(($event.target as HTMLInputElement).value) })" />
          <template v-if="c.operator === 'between'">
            <span>ile</span>
            <input type="number" class="narrow" :value="c.value_to" aria-label="Üst değer" @input="update(i, { value_to: Number(($event.target as HTMLInputElement).value) })" />
          </template>
        </div>

        <div v-else class="body row">
          <select :value="c.occurred === false ? 'no' : 'yes'" aria-label="Yaptı mı" class="narrow" @change="update(i, { occurred: ($event.target as HTMLSelectElement).value === 'yes' })">
            <option value="yes">yaptı</option>
            <option value="no">yapmadı</option>
          </select>
          <select :value="c.event" aria-label="Eylem" @change="update(i, { event: ($event.target as HTMLSelectElement).value })">
            <option v-for="ev in catalog.events" :key="ev" :value="ev">{{ eventLabel[ev]?.[0] ?? ev }}</option>
          </select>
          <select :value="c.category ?? ''" aria-label="Kategori" @change="update(i, { category: ($event.target as HTMLSelectElement).value || undefined })">
            <option value="">her kategoride</option>
            <option v-for="cat in catalog.categories" :key="cat" :value="cat">{{ categoryLabel[cat] ?? cat }}</option>
          </select>
          <span>son</span>
          <input type="number" class="tiny" min="1" :max="catalog.max_days" :value="c.within_days ?? ''" placeholder="∞" aria-label="Gün" @input="update(i, { within_days: optionalNumber(($event.target as HTMLInputElement).value) })" />
          <span>günde</span>
          <template v-if="c.occurred !== false">
            <span>en az</span>
            <input type="number" class="tiny" min="1" :value="c.min_count ?? 1" aria-label="En az kaç kez" @input="update(i, { min_count: optionalNumber(($event.target as HTMLInputElement).value) })" />
            <span>kez</span>
          </template>
        </div>
      </li>
    </ol>
    <button type="button" @click="add">Koşul ekle</button>
  </div>
</template>

<style scoped>
.editor { display: grid; gap: 0.75rem; }
.match select { width: auto; }
.conds { list-style: none; padding: 0; margin: 0; display: grid; gap: 0.5rem; }
.cond { border: 1px solid var(--hair); border-radius: var(--r-field); padding: 0.65rem; background: var(--wash); }
.cond > .row select { width: auto; min-width: 14rem; }
.body { margin-top: 0.5rem; display: flex; gap: 0.5rem; flex-wrap: wrap; align-items: center; }
.body select { width: auto; }
.narrow { width: 8rem !important; }
.tiny { width: 4.5rem; }
.chips { display: flex; flex-wrap: wrap; gap: 0.3rem; }
.chip {
  display: inline-flex; align-items: center; gap: 0.3rem; margin: 0; cursor: pointer; font-weight: 500;
  padding: 0.25rem 0.55rem; border: 1px solid var(--hair); border-radius: 999px; background: var(--sheet);
}
.chip.on { background: var(--ink); color: #fff; border-color: var(--ink); }
.chip input { position: absolute; opacity: 0; width: 1px; height: 1px; }
.chip:focus-within { outline: 2px solid var(--flight); outline-offset: 1px; }
</style>
