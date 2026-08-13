<script setup>
import { onActivated, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { request, clearSession } from '../api'
import { useToast } from '../composables/useToast'

const router = useRouter(), toast = useToast()
const content = ref(''), submitting = ref(false), processing = ref(false), batch = ref(null), pendingDuplicates = ref(0)
let timer
const statusMap = { pending:'等待处理', retry:'等待重试', processing:'处理中', completed:'处理完成', failed:'处理失败' }

async function loadDuplicates() { try { pendingDuplicates.value = (await request('/duplicate-candidates')).items?.length || 0 } catch (_) {} }
function logout() { clearSession(); router.replace('/login') }
function openItem(item) { if (item.entity_id && item.detected_type !== 'unknown') router.push(`/entities/${item.detected_type}/${item.entity_id}`) }
function stopPolling() { clearInterval(timer); timer = null }
async function loadBatch(id) {
  try {
    batch.value = await request(`/ingestion-batches/${id}`)
    processing.value = ['pending','retry','processing'].includes(batch.value.status)
    if (!processing.value) { stopPolling(); if (batch.value.status === 'completed') { content.value=''; loadDuplicates() } }
  } catch (error) { stopPolling(); processing.value=false; toast.show(error.message) }
}
function poll(id) { stopPolling(); loadBatch(id); timer=setInterval(()=>loadBatch(id),1800) }
async function submit() {
  if (!content.value.trim() || submitting.value) return
  submitting.value=true; batch.value=null
  try {
    const data=await request('/ingestion-batches',{method:'POST',data:{content:content.value.trim(),source_name:'H5录入'}})
    batch.value={id:data.batch_id,status:data.status}; processing.value=true; poll(data.batch_id)
  } catch(error) { toast.show(error.message) } finally { submitting.value=false }
}
async function retry() { try { await request(`/ingestion-batches/${batch.value.id}/retry`,{method:'POST'}); processing.value=true; poll(batch.value.id) } catch(error){ toast.show(error.message) } }
onMounted(loadDuplicates); onActivated(loadDuplicates); onBeforeUnmount(stopPolling)
</script>

<template>
  <div class="page">
    <header class="hero"><div class="hero-head"><h1>快速录入项目与需求</h1><div><button @click="router.push('/duplicates')">疑似重复</button><button @click="logout">退出</button></div></div><p>直接粘贴微信中的一条或多条信息，系统会自动拆分并判断投资需求或项目需求。</p></header>
    <aside v-if="pendingDuplicates" class="duplicate-alert" @click="router.push('/duplicates')"><div><strong>发现 {{pendingDuplicates}} 条疑似重复信息待处理</strong><small>请确认合并，或保留为两条独立信息</small></div><b>去处理 ›</b></aside>
    <section class="panel input-panel"><textarea v-model="content" maxlength="2000000" placeholder="例如：&#10;1. 上市公司拟收购固态电池项目……&#10;2. 某智能制造项目寻求并购……"></textarea><div class="submit-row"><span>{{content.length}} 字</span><button class="primary-btn" :disabled="!content.trim()||submitting" @click="submit">{{submitting?'提交中…':'提交录入'}}</button></div></section>
    <section v-if="batch" class="panel result-panel"><div class="result-head"><h2>处理结果</h2><span class="status" :class="batch.status">{{statusMap[batch.status]||batch.status}}</span></div>
      <div v-if="processing" class="processing"><t-loading theme="circular" /> 正在切分、识别并保存，请稍候…</div>
      <template v-if="batch.status==='completed'"><div class="summary-grid"><div><b>{{batch.total_items}}</b><span>信息项</span></div><div class="buy"><b>{{batch.buy_count}}</b><span>投资需求</span></div><div class="sell"><b>{{batch.sell_count}}</b><span>项目需求</span></div><div><b>{{batch.unknown_count}}</b><span>待处理</span></div></div><div v-if="batch.duplicate_count" class="duplicate-tip">已合并 {{batch.duplicate_count}} 条完全重复信息</div>
        <article v-for="(item,index) in batch.items" :key="item.id" class="parsed-item" :class="{clickable:item.entity_id&&item.detected_type!=='unknown'}" @click="openItem(item)"><div class="parsed-head"><span class="number">{{index+1}}</span><span class="type" :class="item.detected_type">{{item.detected_type==='buy_demand'?'投资需求':item.detected_type==='sell_project'?'项目需求':'待处理'}}</span><span v-if="item.was_duplicate" class="duplicate-label">已合并</span></div><h3>{{item.title||item.extraction_json?.title||'未命名信息'}}</h3><p>{{item.raw_text}}</p></article>
      </template><div v-if="batch.status==='failed'" class="failed"><span>{{batch.error_message||'处理失败，请稍后重试'}}</span><button class="ghost-btn" @click="retry">重新处理</button></div>
    </section><div v-else class="empty">提交后的结构化结果会显示在这里</div>
  </div>
</template>

<style scoped>
.hero { padding:8px 4px 20px; }.hero h1{margin:0;color:#0f172a;font-size:27px}.hero-head{display:flex;align-items:center;justify-content:space-between;gap:12px}.hero-head>div{display:flex;gap:12px}.hero button{border:0;background:none;color:#0052d9;font-size:13px}.hero p{margin:8px 0 0;color:#64748b;font-size:15px;line-height:1.65}.duplicate-alert{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:16px;padding:15px;border:1px solid #f3c779;border-radius:12px;background:#fff8e8;color:#8d5a00}.duplicate-alert small{display:block;margin-top:5px;color:#a87520}.duplicate-alert b{flex:none;color:#d97706;font-size:13px}.input-panel{padding:8px 16px 16px}.input-panel textarea{display:block;width:100%;min-height:210px;max-height:50vh;padding:14px 4px;border:0;outline:0;resize:vertical;line-height:1.65}.submit-row,.result-head,.parsed-head{display:flex;align-items:center}.submit-row{justify-content:space-between;border-top:1px solid #eef2f7;padding-top:13px;color:#94a3b8;font-size:13px}.result-head{justify-content:space-between}.result-head h2{margin:0;font-size:20px}.status{padding:4px 9px;border-radius:999px;background:#eef2f7;font-size:12px}.status.completed{background:#e8f8f2;color:#008858}.status.failed{background:#fff0ef;color:#d54941}.processing{display:flex;align-items:center;gap:10px;padding:30px 0;color:#64748b}.summary-grid{display:grid;grid-template-columns:repeat(4,1fr);gap:7px;margin:18px 0}.summary-grid div{display:flex;flex-direction:column;align-items:center;padding:14px 3px;border-radius:10px;background:#f8fafc;color:#64748b;font-size:12px}.summary-grid b{margin-bottom:4px;color:#1e293b;font-size:23px}.summary-grid .buy b{color:#0052d9}.summary-grid .sell b{color:#00a870}.duplicate-tip{padding:11px 13px;border-radius:9px;background:#fff7e8;color:#8d5a00;font-size:13px}.parsed-item{padding:18px 0;border-top:1px solid #eef2f7}.parsed-item.clickable{cursor:pointer}.number{display:grid;width:26px;height:26px;place-items:center;border-radius:50%;background:#eef4ff;color:#0052d9;font-size:12px}.type{padding:4px 8px;border-radius:999px;background:#fff3e6;color:#b86b00;font-size:12px}.type.buy_demand{background:#eef4ff;color:#0052d9}.type.sell_project{background:#e8f8f2;color:#008858}.duplicate-label{color:#d97706;font-size:12px}.parsed-item h3{margin:11px 0 6px;color:#172033;font-size:17px}.parsed-item p{display:-webkit-box;overflow:hidden;margin:0;color:#64748b;font-size:14px;line-height:1.6;-webkit-box-orient:vertical;-webkit-line-clamp:3;white-space:pre-wrap}.failed{display:flex;flex-direction:column;gap:14px;color:#d54941}
</style>
