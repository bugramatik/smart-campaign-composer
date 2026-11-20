import { createRouter, createWebHistory } from 'vue-router'
import NewCampaign from './views/NewCampaign.vue'
import CampaignView from './views/CampaignView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'new', component: NewCampaign },
    { path: '/campaigns/:id', name: 'campaign', component: CampaignView, props: (r) => ({ id: Number(r.params.id) }) },
  ],
})
