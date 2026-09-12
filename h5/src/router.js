import { createRouter, createWebHistory } from 'vue-router'
import LoginView from './views/LoginView.vue'
import IngestView from './views/IngestView.vue'
import EntityListView from './views/EntityListView.vue'
import EntityDetailView from './views/EntityDetailView.vue'
import DuplicatesView from './views/DuplicatesView.vue'
import IngestionHistoryView from './views/IngestionHistoryView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/ingest' },
    { path: '/login', component: LoginView, meta: { public: true, title: '登录' } },
    { path: '/ingest', component: IngestView, meta: { tab: 'ingest', title: '录入' } },
    { path: '/buy-demands', component: EntityListView, props: { entityType: 'buy_demand' }, meta: { tab: 'buy', title: '投资需求' } },
    { path: '/sell-projects', component: EntityListView, props: { entityType: 'sell_project' }, meta: { tab: 'sell', title: '项目需求' } },
    { path: '/entities/:type/:id', component: EntityDetailView, meta: { title: '需求详情' } },
    { path: '/duplicates', component: DuplicatesView, meta: { title: '疑似重复' } },
    { path: '/ingestion-history', component: IngestionHistoryView, meta: { title: '录入记录' } },
  ],
})

router.beforeEach(to => {
  document.title = `${to.meta.title || '项目匹配'} - MatchMind`
  if (!to.meta.public && !localStorage.getItem('token')) return '/login'
  if (to.path === '/login' && localStorage.getItem('token')) return '/ingest'
})

export default router
