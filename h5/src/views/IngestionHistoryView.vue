<script setup>
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { request } from '../api'
import { formatDate } from '../utils'
import { useToast } from '../composables/useToast'

const toast = useToast()
const loading = ref(true)
const items = ref([])
const total = ref(0)
const expandedId = ref(null)
const details = ref({})
const loadingDetail = ref(null)
const retrying = ref(null)
let timer

const statusMeta = {
  pending: ['等待整理', 'waiting'],
  retry: ['等待重试', 'waiting'],
  processing: ['正在整理', 'processing'],
  completed: ['整理完成', 'completed'],
  failed: ['整理失败', 'failed'],
}

function decorate(item) {
  const meta = statusMeta[item.status] || [item.status, 'waiting']
  return { ...item, statusText: meta[0], statusClass: meta[1], createdText: formatDate(item.created_at) }
}

async function reload(silent = false) {
  if (!silent) loading.value = true
  try {
    const data = await request('/ingestion-batches?page=1&page_size=100')
    items.value = (data.items || []).map(decorate)
    total.value = data.total || 0
    if (expandedId.value && ['pending', 'retry', 'processing'].includes(details.value[expandedId.value]?.status)) {
      await loadDetail(expandedId.value, true)
    }
  } catch (error) {
    if (!silent) toast.show(error.message)
  } finally {
    if (!silent) loading.value = false
  }
}

async function loadDetail(id, silent = false) {
  if (!silent) loadingDetail.value = id
  try {
    const detail = await request(`/ingestion-batches/${id}`)
    details.value = { ...details.value, [id]: detail }
  } catch (error) {
    if (!silent) toast.show(error.message)
  } finally {
    if (!silent) loadingDetail.value = null
  }
}

async function toggle(item) {
  if (expandedId.value === item.id) {
    expandedId.value = null
    return
  }
  expandedId.value = item.id
  if (!details.value[item.id]) await loadDetail(item.id)
}

async function retry(id) {
  retrying.value = id
  try {
    await request(`/ingestion-batches/${id}/retry`, { method: 'POST' })
    toast.show('已重新提交，系统会继续整理')
    await loadDetail(id, true)
    await reload(true)
  } catch (error) {
    toast.show(error.message)
  } finally {
    retrying.value = null
  }
}

onMounted(async () => {
  await reload()
  timer = setInterval(() => {
    if (items.value.some(item => ['pending', 'retry', 'processing'].includes(item.status))) reload(true)
  }, 4000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div class="page no-tabs history-page">
    <header class="page-head"><button aria-label="返回" @click="$router.back()">‹</button><h1>录入记录</h1><span>{{ total }}</span></header>
    <div v-if="loading" class="loading"><t-loading theme="circular" /> 加载中…</div>
    <article v-for="item in items" :key="item.id" class="panel history-card">
      <button class="card-main" @click="toggle(item)">
        <div class="card-meta"><time>{{ item.createdText }}</time><span class="status" :class="item.statusClass">{{ item.statusText }}</span></div>
        <p>{{ item.raw_text_preview || '未提供原始信息' }}</p>
        <div class="counts">
          <span v-if="item.status === 'completed'">共 {{ item.total_items }} 条</span>
          <span v-if="item.buy_count" class="buy">投资需求 {{ item.buy_count }}</span>
          <span v-if="item.sell_count" class="sell">项目需求 {{ item.sell_count }}</span>
          <b>{{ expandedId === item.id ? '收起' : '查看' }} ›</b>
        </div>
      </button>
      <section v-if="expandedId === item.id" class="detail">
        <div v-if="loadingDetail === item.id" class="detail-loading"><t-loading theme="circular" /> 正在加载…</div>
        <template v-else-if="details[item.id]">
          <div v-if="['pending','retry','processing'].includes(details[item.id].status)" class="progress"><t-loading theme="circular" /> {{ statusMeta[details[item.id].status]?.[0] }}，可以关闭页面后稍后回来查看</div>
          <div v-if="details[item.id].status === 'failed'" class="failure"><span>{{ details[item.id].error_message || '整理失败，请稍后重试' }}</span><button class="ghost-btn" :disabled="retrying === item.id" @click="retry(item.id)">{{ retrying === item.id ? '提交中…' : '重新处理' }}</button></div>
          <h2>原始信息</h2><p class="raw-text">{{ details[item.id].raw_text }}</p>
          <template v-if="details[item.id].items?.length">
            <h2>整理结果（{{ details[item.id].items.length }}）</h2>
            <div v-for="(result,index) in details[item.id].items" :key="result.id" class="result-item">
              <div><i>{{ index + 1 }}</i><span :class="result.detected_type">{{ result.detected_type === 'buy_demand' ? '投资需求' : result.detected_type === 'sell_project' ? '项目需求' : '未识别' }}</span><em v-if="result.was_duplicate">已合并</em></div>
              <b>{{ result.title || '未命名信息' }}</b><p>{{ result.raw_text }}</p>
            </div>
          </template>
        </template>
      </section>
    </article>
    <div v-if="!loading && !items.length" class="empty">还没有录入记录</div>
  </div>
</template>

<style scoped>
.page-head{display:grid;grid-template-columns:40px 1fr 40px;align-items:center;margin-bottom:18px}.page-head button{border:0;background:none;color:#334155;font-size:32px}.page-head h1{margin:0;text-align:center;font-size:21px}.page-head>span{color:#94a3b8;text-align:right;font-size:13px}.history-card{padding:0;overflow:hidden}.card-main{display:block;width:100%;padding:17px 18px;border:0;background:#fff;text-align:left}.card-meta{display:flex;align-items:center;justify-content:space-between;gap:12px}.card-meta time{color:#64748b;font-size:13px}.status{flex:none;padding:4px 9px;border-radius:999px;background:#eef2f7;color:#64748b;font-size:12px}.status.processing{background:#eef4ff;color:#0052d9}.status.completed{background:#e8f8f2;color:#008858}.status.failed{background:#fff0ef;color:#d54941}.card-main>p{display:-webkit-box;overflow:hidden;margin:13px 0;color:#263449;font-size:15px;line-height:1.55;-webkit-box-orient:vertical;-webkit-line-clamp:2;white-space:pre-wrap}.counts{display:flex;align-items:center;gap:10px;color:#94a3b8;font-size:12px}.counts .buy{color:#0052d9}.counts .sell{color:#008858}.counts b{margin-left:auto;color:#475569;font-weight:500}.detail{padding:0 18px 18px;border-top:1px solid #eef2f7}.detail h2{margin:17px 0 8px;color:#334155;font-size:14px}.raw-text{max-height:230px;overflow:auto;margin:0;padding:12px;border-radius:9px;background:#f8fafc;color:#475569;font-size:13px;line-height:1.65;white-space:pre-wrap}.detail-loading,.progress{display:flex;align-items:center;gap:8px;padding:18px 0;color:#64748b;font-size:13px}.failure{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:15px 0;color:#d54941;font-size:13px}.result-item{padding:13px 0;border-top:1px solid #eef2f7}.result-item>div{display:flex;align-items:center;gap:7px}.result-item i{display:grid;width:23px;height:23px;place-items:center;border-radius:50%;background:#eef2f7;color:#64748b;font-size:11px;font-style:normal}.result-item span{padding:3px 7px;border-radius:999px;background:#f1f5f9;color:#64748b;font-size:11px}.result-item span.buy_demand{background:#eef4ff;color:#0052d9}.result-item span.sell_project{background:#e8f8f2;color:#008858}.result-item em{color:#d97706;font-size:11px;font-style:normal}.result-item>b{display:block;margin-top:9px;color:#263449;font-size:14px}.result-item>p{display:-webkit-box;overflow:hidden;margin:6px 0 0;color:#64748b;font-size:13px;line-height:1.55;-webkit-box-orient:vertical;-webkit-line-clamp:3;white-space:pre-wrap}
</style>
