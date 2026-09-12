<script setup>
import { onActivated, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { request, clearSession } from '../api'
import { useToast } from '../composables/useToast'

const router = useRouter(), toast = useToast()
const currentUser = JSON.parse(localStorage.getItem('user') || '{}')
const storageSuffix = currentUser.id || currentUser.username || 'current'
const DRAFT_KEY = `matchmind_ingest_draft_${storageSuffix}`
const REQUEST_KEY = `matchmind_ingest_request_${storageSuffix}`
const REQUEST_CONTENT_KEY = `matchmind_ingest_request_content_${storageSuffix}`
const LATEST_BATCH_KEY = `matchmind_latest_batch_${storageSuffix}`
const content = ref(localStorage.getItem(DRAFT_KEY) || ''), submitting = ref(false), processing = ref(false), batch = ref(null), pendingDuplicates = ref(0)
let timer, activeSubmittedContent = ''
const statusMap = { pending:'等待整理', retry:'等待重试', processing:'正在整理', completed:'整理完成', failed:'整理失败' }

async function loadDuplicates() { try { pendingDuplicates.value = (await request('/duplicate-candidates')).items?.length || 0 } catch (_) {} }
function logout() { clearSession(); router.replace('/login') }
function openItem(item) { if (item.entity_id && item.detected_type !== 'unknown') router.push(`/entities/${item.detected_type}/${item.entity_id}`) }
function stopPolling() { clearInterval(timer); timer = null }
async function loadBatch(id, quiet = false) {
  try {
    const loaded = await request(`/ingestion-batches/${id}`)
    batch.value = loaded
    if (!activeSubmittedContent) activeSubmittedContent = loaded.raw_text || ''
    processing.value = ['pending','retry','processing'].includes(loaded.status)
    if (!processing.value) {
      stopPolling()
      if (loaded.status === 'completed') {
        if (content.value.trim() === activeSubmittedContent.trim()) content.value = ''
        loadDuplicates()
      }
    }
    return true
  } catch (error) {
    stopPolling(); processing.value=false; localStorage.removeItem(LATEST_BATCH_KEY)
    if (!quiet) toast.show(error.message)
    return false
  }
}
function poll(id) { stopPolling(); loadBatch(id); timer=setInterval(()=>loadBatch(id, true),1800) }
function newRequestId() { return globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}` }
async function restoreLatestBatch() {
  const savedId = localStorage.getItem(LATEST_BATCH_KEY)
  if (savedId) {
    const found = await loadBatch(savedId, true)
    if (found && processing.value) poll(savedId)
    if (found) return
  }
  try {
    const data = await request('/ingestion-batches?page=1&page_size=20')
    const active = (data.items || []).find(item => ['pending','retry','processing'].includes(item.status))
    if (active) {
      localStorage.setItem(LATEST_BATCH_KEY, active.id)
      activeSubmittedContent = ''
      poll(active.id)
    }
  } catch (_) {}
}
async function submit() {
  if (!content.value.trim() || submitting.value) return
  submitting.value=true; batch.value=null
  try {
    const submittedContent = content.value.trim()
    let clientRequestId = localStorage.getItem(REQUEST_KEY)
    if (!clientRequestId || localStorage.getItem(REQUEST_CONTENT_KEY) !== submittedContent) clientRequestId = newRequestId()
    localStorage.setItem(REQUEST_KEY, clientRequestId)
    localStorage.setItem(REQUEST_CONTENT_KEY, submittedContent)
    const data=await request('/ingestion-batches',{method:'POST',data:{content:submittedContent,source_name:'H5录入',client_request_id:clientRequestId}})
    activeSubmittedContent = submittedContent
    localStorage.setItem(LATEST_BATCH_KEY, data.batch_id)
    localStorage.removeItem(REQUEST_KEY)
    localStorage.removeItem(REQUEST_CONTENT_KEY)
    batch.value={id:data.batch_id,status:data.status}; processing.value=true; poll(data.batch_id)
  } catch(error) { toast.show(error.message) } finally { submitting.value=false }
}
async function retry() { try { await request(`/ingestion-batches/${batch.value.id}/retry`,{method:'POST'}); processing.value=true; poll(batch.value.id) } catch(error){ toast.show(error.message) } }
watch(content, value => {
  if (value) localStorage.setItem(DRAFT_KEY, value)
  else localStorage.removeItem(DRAFT_KEY)
  const requestContent = localStorage.getItem(REQUEST_CONTENT_KEY)
  if (requestContent && requestContent !== value.trim()) {
    localStorage.removeItem(REQUEST_KEY)
    localStorage.removeItem(REQUEST_CONTENT_KEY)
  }
})
onMounted(async () => { await Promise.all([loadDuplicates(), restoreLatestBatch()]) })
onActivated(loadDuplicates); onBeforeUnmount(stopPolling)
</script>

<template>
  <div class="page">
    <header class="hero"><div class="hero-head"><h1>录入信息</h1><div class="hero-actions"><button @click="router.push('/ingestion-history')">录入记录</button><button class="logout-btn" @click="logout">退出登录</button></div></div><p>粘贴项目或投资需求，系统将自动整理入库</p></header>
    <aside v-if="pendingDuplicates" class="duplicate-alert" role="button" tabindex="0" @click="router.push('/duplicates')" @keydown.enter="router.push('/duplicates')"><div><strong>有 {{pendingDuplicates}} 条信息可能重复</strong><small>请确认是合并，还是分别保留</small></div><b>去确认 ›</b></aside>
    <section class="panel input-panel"><label for="ingest-content">粘贴原始信息</label><textarea id="ingest-content" v-model="content" maxlength="2000000" placeholder="例如：&#10;1. 上市公司拟收购固态电池项目……&#10;2. 某智能制造项目寻求并购……"></textarea><div class="submit-row"><span>{{content.length}} 字</span><button class="primary-btn" :disabled="!content.trim()||submitting" @click="submit">{{submitting?'正在提交…':'识别并保存'}}</button></div></section>
    <section v-if="batch" class="panel result-panel"><div class="result-head"><h2>整理结果</h2><span class="status" :class="batch.status">{{statusMap[batch.status]||batch.status}}</span></div>
      <div v-if="processing" class="processing"><t-loading theme="circular" /> 正在整理信息，请稍候…</div>
      <template v-if="batch.status==='completed'"><div class="summary-grid"><div><b>{{batch.total_items}}</b><span>信息项</span></div><div class="buy"><b>{{batch.buy_count}}</b><span>投资需求</span></div><div class="sell"><b>{{batch.sell_count}}</b><span>项目需求</span></div><div><b>{{batch.unknown_count}}</b><span>待处理</span></div></div><div v-if="batch.duplicate_count" class="duplicate-tip">已合并 {{batch.duplicate_count}} 条完全重复信息</div>
        <article v-for="(item,index) in batch.items" :key="item.id" class="parsed-item" :class="{clickable:item.entity_id&&item.detected_type!=='unknown'}" @click="openItem(item)"><div class="parsed-head"><span class="number">{{index+1}}</span><span class="type" :class="item.detected_type">{{item.detected_type==='buy_demand'?'投资需求':item.detected_type==='sell_project'?'项目需求':'待处理'}}</span><span v-if="item.was_duplicate" class="duplicate-label">已合并</span></div><h3>{{item.title||item.extraction_json?.title||'未命名信息'}}</h3><p>{{item.raw_text}}</p></article>
      </template><div v-if="batch.status==='failed'" class="failed"><span>{{batch.error_message||'处理失败，请稍后重试'}}</span><button class="ghost-btn" @click="retry">重新处理</button></div>
    </section><div v-else class="empty">整理结果会显示在这里</div>
  </div>
</template>

<style scoped>
.hero { padding:8px 4px 20px; }.hero h1{margin:0;color:#0f172a;font-size:27px}.hero-head{display:flex;align-items:center;justify-content:space-between;gap:12px}.hero-head>div{display:flex;gap:12px}.hero button{border:0;background:none;color:#0052d9;font-size:13px}.hero p{margin:8px 0 0;color:#64748b;font-size:15px;line-height:1.65}.duplicate-alert{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:16px;padding:15px;border:1px solid #f3c779;border-radius:12px;background:#fff8e8;color:#8d5a00}.duplicate-alert small{display:block;margin-top:5px;color:#a87520}.duplicate-alert b{flex:none;color:#d97706;font-size:13px}.input-panel{padding:8px 16px 16px}.input-panel textarea{display:block;width:100%;min-height:210px;max-height:50vh;padding:14px 4px;border:0;outline:0;resize:vertical;line-height:1.65}.submit-row,.result-head,.parsed-head{display:flex;align-items:center}.submit-row{justify-content:space-between;border-top:1px solid #eef2f7;padding-top:13px;color:#94a3b8;font-size:13px}.result-head{justify-content:space-between}.result-head h2{margin:0;font-size:20px}.status{padding:4px 9px;border-radius:999px;background:#eef2f7;font-size:12px}.status.completed{background:#e8f8f2;color:#008858}.status.failed{background:#fff0ef;color:#d54941}.processing{display:flex;align-items:center;gap:10px;padding:30px 0;color:#64748b}.summary-grid{display:grid;grid-template-columns:repeat(4,1fr);gap:7px;margin:18px 0}.summary-grid div{display:flex;flex-direction:column;align-items:center;padding:14px 3px;border-radius:10px;background:#f8fafc;color:#64748b;font-size:12px}.summary-grid b{margin-bottom:4px;color:#1e293b;font-size:23px}.summary-grid .buy b{color:#0052d9}.summary-grid .sell b{color:#00a870}.duplicate-tip{padding:11px 13px;border-radius:9px;background:#fff7e8;color:#8d5a00;font-size:13px}.parsed-item{padding:18px 0;border-top:1px solid #eef2f7}.parsed-item.clickable{cursor:pointer}.number{display:grid;width:26px;height:26px;place-items:center;border-radius:50%;background:#eef4ff;color:#0052d9;font-size:12px}.type{padding:4px 8px;border-radius:999px;background:#fff3e6;color:#b86b00;font-size:12px}.type.buy_demand{background:#eef4ff;color:#0052d9}.type.sell_project{background:#e8f8f2;color:#008858}.duplicate-label{color:#d97706;font-size:12px}.parsed-item h3{margin:11px 0 6px;color:#172033;font-size:17px}.parsed-item p{display:-webkit-box;overflow:hidden;margin:0;color:#64748b;font-size:14px;line-height:1.6;-webkit-box-orient:vertical;-webkit-line-clamp:3;white-space:pre-wrap}.failed{display:flex;flex-direction:column;gap:14px;color:#d54941}

/* Mobile-first header: keep the task title stable and move account actions into a menu. */
.hero-head{display:grid;grid-template-columns:minmax(0,1fr) auto;align-items:center;gap:10px}
.hero h1{min-width:0;white-space:nowrap}
.hero p{max-width:none}
.hero-actions{display:flex;align-items:center;gap:8px}.hero-actions button{padding:9px 2px!important;white-space:nowrap}.logout-btn{color:#64748b!important}
.duplicate-alert{cursor:pointer}
.duplicate-alert b{white-space:nowrap}
.input-panel{padding-top:14px}
.input-panel label{display:block;color:#334155;font-size:14px;font-weight:650}
.input-panel textarea{min-height:190px;padding-top:12px}
</style>
