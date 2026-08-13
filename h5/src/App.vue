<script setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import ToastMessage from './components/ToastMessage.vue'

const route = useRoute()
const router = useRouter()
const showTabs = computed(() => route.meta.tab && route.path !== '/login')
const tabs = [
  { value: 'ingest', label: '录入', path: '/ingest', icon: '＋' },
  { value: 'buy', label: '投资需求', path: '/buy-demands', icon: '投' },
  { value: 'sell', label: '项目需求', path: '/sell-projects', icon: '项' },
]
</script>

<template>
  <ToastMessage />
  <main class="app-shell"><router-view /></main>
  <nav v-if="showTabs" class="bottom-nav safe-bottom">
    <button v-for="tab in tabs" :key="tab.value" :class="[tab.value, { active: route.meta.tab === tab.value }]" @click="router.push(tab.path)">
      <i>{{tab.icon}}</i><span>{{ tab.label }}</span>
    </button>
  </nav>
</template>
