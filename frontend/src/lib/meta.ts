import { ref } from 'vue'
import { api } from './api'
import type { Campaign, Meta } from './types'

// Shared app state: the catalog/limits (loaded once) and the sidebar list.
export const meta = ref<Meta | null>(null)
export const campaigns = ref<Campaign[]>([])

let metaLoad: Promise<Meta> | null = null

export function loadMeta(): Promise<Meta> {
  metaLoad ??= api.meta().then((m) => (meta.value = m))
  return metaLoad
}

export async function refreshCampaigns(): Promise<void> {
  campaigns.value = await api.list()
}

// upsert keeps the sidebar in step with a campaign the page just changed.
export function upsert(c: Campaign): void {
  const i = campaigns.value.findIndex((x) => x.id === c.id)
  if (i >= 0) campaigns.value[i] = c
  else campaigns.value.unshift(c)
}
