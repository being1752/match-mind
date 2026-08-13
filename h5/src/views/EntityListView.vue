<script setup>
import { computed, onActivated, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { request } from '../api'
import { formatDate, ensureArray } from '../utils'
import { useToast } from '../composables/useToast'

const props = defineProps({ entityType: { type:String, required:true } })
const router=useRouter(), toast=useToast(), query=ref(''), items=ref([]), loading=ref(false)
const isBuy=computed(()=>props.entityType==='buy_demand')
async function reload(){ loading.value=true; try{const path=isBuy.value?'buy-demands':'sell-projects'; const data=await request(`/${path}?q=${encodeURIComponent(query.value)}&page_size=100`); items.value=(data.items||[]).map((item,index)=>({...item,industries:ensureArray(item.industries),displayNumber:String(index+1).padStart(2,'0'),firstCreatedText:formatDate(item.created_at)}))}catch(error){toast.show(error.message)}finally{loading.value=false} }
function open(item){router.push(`/entities/${props.entityType}/${item.id}`)}
watch(()=>props.entityType,()=>{query.value='';reload()}); onMounted(reload); onActivated(reload)
</script>

<template>
  <div class="page list-page" :class="isBuy?'buy-theme':'sell-theme'">
    <div class="search-wrap"><input v-model="query" :placeholder="isBuy?'搜索投资需求':'搜索项目需求'" @keyup.enter="reload" /><button @click="reload">搜索</button></div>
    <div v-if="loading" class="loading"><t-loading theme="circular" /> 加载中…</div>
    <article v-for="item in items" :key="item.id" class="panel entity-card" @click="open(item)"><div class="display-number">{{item.displayNumber}}</div><div class="entity-content"><h2>{{item.title}}</h2><div v-if="item.buyer_name||item.company_name" class="entity-sub">{{item.buyer_name||item.company_name}}</div><div class="tags"><span v-for="tag in item.industries" :key="tag" class="tag">{{tag}}</span></div><p>{{item.summary||'暂无摘要'}}</p><div class="first-created">首次录入：{{item.firstCreatedText}}</div></div></article>
    <div v-if="!loading&&!items.length" class="empty">暂无{{isBuy?'投资需求':'项目需求'}}</div>
  </div>
</template>

<style scoped>
.list-page{--theme:#0052d9;--theme-soft:#eef4ff;padding-top:14px}.list-page.sell-theme{--theme:#00a870;--theme-soft:#e8f8f2}.search-wrap{position:sticky;z-index:5;top:0;display:flex;gap:8px;padding:4px 0 12px;background:#f5f7fa}.search-wrap input{flex:1;min-width:0;height:44px;padding:0 14px;border:1px solid #dfe5ed;border-radius:12px;outline:none}.search-wrap input:focus{border-color:var(--theme)}.search-wrap button{padding:0 15px;border:0;border-radius:10px;background:var(--theme);color:#fff}.entity-card{display:flex;align-items:flex-start;gap:14px;border-top:3px solid var(--theme);cursor:pointer}.display-number{flex:none;width:42px;color:var(--theme);font-size:26px;font-weight:700;line-height:1.1}.entity-content{flex:1;min-width:0}.entity-content h2{margin:0;color:#172033;font-size:18px;line-height:1.45}.entity-sub{margin-top:5px;color:var(--theme);font-size:14px}.list-page :deep(.tag){background:var(--theme-soft);color:var(--theme)}.entity-content p{display:-webkit-box;overflow:hidden;margin:12px 0 0;color:#64748b;font-size:14px;line-height:1.6;-webkit-box-orient:vertical;-webkit-line-clamp:3}.first-created{margin-top:13px;padding-top:12px;border-top:1px solid #eef2f7;color:#94a3b8;font-size:12px}
</style>
