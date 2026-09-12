<script setup>
import { onMounted, ref } from 'vue'
import { request } from '../api'
import { formatDate, formatMoney } from '../utils'
import { useToast } from '../composables/useToast'

const toast = useToast()
const loading = ref(false)
const items = ref([])
const resolving = ref(null)

function disclosed(value) {
  if (value === null || value === undefined || value === '') return false
  if (Array.isArray(value)) return value.some(disclosed)
  if (typeof value === 'object') return Object.values(value).some(disclosed)
  return true
}

function valueText(value) {
  if (!disclosed(value)) return ''
  if (Array.isArray(value)) return value.filter(disclosed).map(valueText).sort((a, b) => a.localeCompare(b, 'zh-CN')).join('、')
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'object') {
    return Object.entries(value)
      .filter(([, item]) => disclosed(item))
      .map(([key, item]) => `${key}：${valueText(item)}`)
      .join('；')
  }
  return String(value)
}

function moneyRange(min, max) {
  if (!disclosed(min)) return disclosed(max) ? `不超过 ${formatMoney(max)}` : ''
  if (!disclosed(max)) return `不少于 ${formatMoney(min)}`
  return Number(min) === Number(max) ? formatMoney(min) : `${formatMoney(min)} 至 ${formatMoney(max)}`
}

function numberRange(min, max) {
  if (!disclosed(min)) return disclosed(max) ? `不超过 ${max}` : ''
  if (!disclosed(max)) return `不少于 ${min}`
  return Number(min) === Number(max) ? String(min) : `${min} 至 ${max}`
}

function percent(value) {
  if (!disclosed(value)) return ''
  const number = Number(value)
  return `${number <= 1 ? number * 100 : number}%`
}

function percentRange(min, max) {
  const left = percent(min)
  const right = percent(max)
  if (!left) return right ? `不超过 ${right}` : ''
  if (!right) return `不少于 ${left}`
  return left === right ? left : `${left} 至 ${right}`
}

function definitions(type) {
  const common = [
    ['主体名称', entity => type === 'buy_demand' ? entity.buyer_name : entity.company_name],
    ['行业', entity => valueText(entity.industries)],
    ['交易方式', entity => valueText(entity.transaction_types)],
  ]
  if (type === 'buy_demand') {
    return [
      ...common,
      ['投资方类型', entity => entity.buyer_type],
      ['标的类型', entity => valueText(entity.target_types)],
      ['投资阶段', entity => valueText(entity.preferred_stages)],
      ['偏好地区', entity => valueText(entity.preferred_regions)],
      ['投资金额', entity => moneyRange(entity.investment_amount_min, entity.investment_amount_max)],
      ['目标营收', entity => moneyRange(entity.target_revenue_min, entity.target_revenue_max)],
      ['目标净利润', entity => moneyRange(entity.target_net_profit_min, entity.target_net_profit_max)],
      ['目标估值', entity => moneyRange(entity.target_valuation_min, entity.target_valuation_max)],
      ['PE 要求', entity => numberRange(entity.pe_min, entity.pe_max)],
      ['上市要求', entity => entity.listed_status_requirement],
      ['最低控股比例', entity => percent(entity.control_ratio_min)],
      ['盈利要求', entity => disclosed(entity.profitability_required) ? (entity.profitability_required ? '要求盈利' : '不限制盈利') : ''],
      ['其他条件', entity => valueText(entity.extra_constraints)],
    ]
  }
  return [
    ...common,
    ['项目类型', entity => valueText(entity.project_types)],
    ['融资轮次', entity => entity.financing_round],
    ['融资金额', entity => moneyRange(entity.financing_amount_min, entity.financing_amount_max)],
    ['营收', entity => moneyRange(entity.revenue_min, entity.revenue_max)],
    ['净利润', entity => moneyRange(entity.net_profit_min, entity.net_profit_max)],
    ['估值', entity => moneyRange(entity.valuation_min, entity.valuation_max)],
    ['PE', entity => numberRange(entity.pe_min, entity.pe_max)],
    ['所在地区', entity => valueText([entity.province, entity.city])],
    ['上市状态', entity => entity.listed_status],
    ['可转让比例', entity => percentRange(entity.transfer_ratio_min, entity.transfer_ratio_max)],
    ['其他信息', entity => valueText(entity.extra_facts)],
  ]
}

function normalize(value) {
  return String(value || '').toLowerCase().replace(/[\s，,。；;：:、（）()\[\]【】]/g, '')
}

function comparisonRows(type, source, candidate) {
  return definitions(type).map(([label, getter], index) => {
    const sourceValue = valueText(getter(source || {}))
    const candidateValue = valueText(getter(candidate || {}))
    if (!sourceValue && !candidateValue) return null
    let status = 'same'
    let statusText = '一致'
    if (sourceValue && !candidateValue) {
      status = 'new'
      statusText = '本次新增'
    } else if (!sourceValue && candidateValue) {
      status = 'existing'
      statusText = '已有信息'
    } else if (normalize(sourceValue) !== normalize(candidateValue)) {
      status = 'different'
      statusText = '存在差异'
    }
    return {
      key: `${label}-${index}`,
      label,
      sourceValue: sourceValue || '未披露',
      candidateValue: candidateValue || '未披露',
      status,
      statusText,
    }
  }).filter(Boolean)
}

function prepareSources(sources) {
  return (sources || []).map(source => ({ ...source, uploadedText: formatDate(source.uploaded_at) }))
}

function prepareItem(item) {
  const sourceEntity = item.source_entity || { title: item.source_title }
  const candidateEntity = item.candidate_entity || { title: item.candidate_title }
  const sourceSources = prepareSources(item.source_sources)
  const candidateSources = prepareSources(item.candidate_sources)
  const rows = comparisonRows(item.source_entity_type, sourceEntity, candidateEntity)
  const supplement = !!item.reason_json?.supplement
  return {
    ...item,
    sourceEntity,
    candidateEntity,
    sourceSources,
    candidateSources,
    rows,
    differenceCount: rows.filter(row => row.status !== 'same').length,
    scoreText: Math.round(Number(item.final_score) * 100),
    typeText: item.source_entity_type === 'buy_demand' ? '投资需求' : '项目需求',
    supplement,
    reasonText: supplement
      ? '核心条件一致，本次内容可能是对已有信息的补充'
      : item.reason_json?.identity_conflict
        ? '主体信息存在冲突，请重点核对差异后再决定'
        : '语义高度相似，请核对双方信息是否指向同一事项',
  }
}

async function reload() {
  loading.value = true
  try {
    const data = await request('/duplicate-candidates')
    items.value = (data.items || []).map(prepareItem)
  } catch (error) {
    toast.show(error.message)
  } finally {
    loading.value = false
  }
}

async function resolve(id, action) {
  if (action === 'merge' && !confirm('确认将本次录入合并到已有信息吗？双方原始信息都会保留在来源记录中。')) return
  resolving.value = id
  try {
    await request(`/duplicate-candidates/${id}/${action}`, { method: 'POST' })
    toast.show(action === 'merge' ? '已合并，双方原始信息均已保留' : '已保留为两条独立信息')
    await reload()
  } catch (error) {
    toast.show(error.message)
  } finally {
    resolving.value = null
  }
}

onMounted(reload)
</script>

<template>
  <div class="page no-tabs">
    <header class="page-head">
      <button @click="$router.back()">‹</button>
      <h1>疑似重复</h1>
      <span></span>
    </header>

    <div v-if="loading" class="loading"><t-loading theme="circular" /> 加载中…</div>

    <article v-for="item in items" :key="item.id" class="panel candidate">
      <div class="candidate-head">
        <div>
          <span class="type-tag" :class="item.source_entity_type">{{ item.typeText }}</span>
          <strong>{{ item.scoreText }}% 相似</strong>
        </div>
        <span v-if="item.differenceCount" class="difference-count">{{ item.differenceCount }} 项需核对</span>
      </div>

      <p class="reason" :class="{ supplement: item.supplement, conflict: item.reason_json?.identity_conflict }">{{ item.reasonText }}</p>

      <section class="entity-card source-card">
        <div class="entity-label">本次录入</div>
        <h2>{{ item.sourceEntity.title || item.source_title || '未命名信息' }}</h2>
        <p class="summary">{{ item.sourceEntity.summary || '暂无摘要，请结合原始信息判断' }}</p>
        <div class="entity-meta">
          <span>录入时间</span>
          <b>{{ item.sourceSources[0]?.uploadedText || formatDate(item.sourceEntity.created_at) }}</b>
        </div>
        <details class="source-details" open>
          <summary>查看原始信息（{{ item.sourceSources.length }} 条）</summary>
          <div v-for="source in item.sourceSources" :key="source.id" class="raw-source">
            <div><b>{{ source.uploader || '历史数据' }}</b><time>{{ source.uploadedText }}</time></div>
            <p>{{ source.raw_text }}</p>
          </div>
          <p v-if="!item.sourceSources.length" class="no-source">暂无原始来源</p>
        </details>
      </section>

      <section class="entity-card existing-card">
        <div class="entity-label">已有信息</div>
        <h2>{{ item.candidateEntity.title || item.candidate_title || '未命名信息' }}</h2>
        <p class="summary">{{ item.candidateEntity.summary || '暂无摘要，请结合原始信息判断' }}</p>
        <div class="entity-meta">
          <span>首次录入</span>
          <b>{{ item.candidateSources[0]?.uploadedText || formatDate(item.candidateEntity.created_at) }}</b>
        </div>
        <details class="source-details" open>
          <summary>查看历史原始信息（{{ item.candidateSources.length }} 条）</summary>
          <div v-for="source in item.candidateSources" :key="source.id" class="raw-source">
            <div><b>{{ source.uploader || '历史数据' }}</b><time>{{ source.uploadedText }}</time></div>
            <p>{{ source.raw_text }}</p>
          </div>
          <p v-if="!item.candidateSources.length" class="no-source">暂无原始来源</p>
        </details>
      </section>

      <h3 class="compare-title">逐项对比</h3>
      <div class="compare-table">
        <div class="compare-row compare-header">
          <b>对比内容</b><b>本次录入</b><b>已有信息</b><b>判断</b>
        </div>
        <div v-for="row in item.rows" :key="row.key" class="compare-row">
          <b class="field-name">{{ row.label }}</b>
          <span class="source-value"><small>本次录入</small>{{ row.sourceValue }}</span>
          <span class="candidate-value"><small>已有信息</small>{{ row.candidateValue }}</span>
          <strong class="status" :class="row.status">{{ row.statusText }}</strong>
        </div>
        <div v-if="!item.rows.length" class="table-empty">暂无可对比的结构化要点，请根据双方原始信息判断</div>
      </div>

      <p class="merge-note">确认合并后，本次内容将补充到已有信息，双方原文及录入时间都会保留。</p>
      <div class="actions">
        <button class="ghost-btn" :disabled="resolving === item.id" @click="resolve(item.id, 'reject')">保留两条</button>
        <button class="primary-btn" :disabled="resolving === item.id" @click="resolve(item.id, 'merge')">
          {{ resolving === item.id ? '处理中…' : '确认合并' }}
        </button>
      </div>
    </article>

    <div v-if="!loading && !items.length" class="empty">暂无待确认的重复信息</div>
  </div>
</template>

<style scoped>
.page-head{display:grid;grid-template-columns:40px 1fr 40px;align-items:center;margin-bottom:18px}.page-head button{border:0;background:none;color:#334155;font-size:32px}.page-head h1{margin:0;text-align:center;font-size:21px}.candidate{padding:17px}.candidate-head,.candidate-head>div,.entity-meta,.raw-source>div,.actions{display:flex;align-items:center}.candidate-head{justify-content:space-between;gap:10px}.candidate-head>div{gap:9px}.candidate-head strong{color:#d97706;font-size:14px}.type-tag{padding:4px 9px;border-radius:999px;background:#eef4ff;color:#0052d9;font-size:12px}.type-tag.sell_project{background:#e8f8f2;color:#008858}.difference-count{color:#c2413a;font-size:12px}.reason{margin:13px 0 16px;padding:11px;border-radius:9px;background:#fff7e8;color:#8d5a00;font-size:13px;line-height:1.55}.reason.supplement{background:#ecfdf5;color:#087f5b}.reason.conflict{background:#fff0ef;color:#c2413a}.entity-card{position:relative;margin-top:12px;padding:15px;border:1px solid #e5eaf0;border-radius:12px;background:#fff}.source-card{border-left:4px solid #d97706}.existing-card{border-left:4px solid #0052d9}.entity-label{color:#94a3b8;font-size:12px;font-weight:600}.source-card .entity-label{color:#b86b00}.existing-card .entity-label{color:#0052d9}.entity-card h2{margin:7px 0 0;color:#172033;font-size:17px;line-height:1.45}.summary{margin:8px 0 0;color:#64748b;font-size:13px;line-height:1.65}.entity-meta{justify-content:space-between;gap:12px;margin-top:11px;color:#94a3b8;font-size:12px}.entity-meta b{color:#64748b;font-weight:500}.source-details{margin-top:12px;border-top:1px solid #eef2f7}.source-details summary{padding-top:11px;color:#475569;font-size:13px;cursor:pointer}.raw-source{margin-top:10px;padding:11px;border-radius:9px;background:#f8fafc}.raw-source>div{justify-content:space-between;gap:10px;color:#64748b;font-size:12px}.raw-source time{color:#94a3b8}.raw-source p{margin:8px 0 0;color:#334155;font-size:13px;line-height:1.65;white-space:pre-wrap;overflow-wrap:anywhere}.no-source{color:#94a3b8;font-size:13px}.compare-title{margin:20px 2px 10px;color:#172033;font-size:16px}.compare-table{overflow:hidden;border:1px solid #e5eaf0;border-radius:10px}.compare-row{display:grid;grid-template-columns:20% 28% 28% 24%;border-bottom:1px solid #e8edf3}.compare-row:last-child{border-bottom:0}.compare-row>*{display:flex;min-width:0;align-items:center;padding:10px 6px;border-right:1px solid #e8edf3;color:#475569;font-size:12px;line-height:1.45;overflow-wrap:anywhere}.compare-row>*:last-child{justify-content:center;border-right:0;text-align:center}.compare-header{background:#f7f9fc}.compare-header>*{color:#64748b;font-weight:600}.field-name{color:#334155}.compare-row small{display:none}.status.same{color:#087f5b}.status.different{color:#c2413a}.status.new{color:#0052d9}.status.existing{color:#8a6d1d}.table-empty{padding:24px;color:#94a3b8;text-align:center;font-size:13px}.merge-note{margin:15px 2px 0;color:#64748b;font-size:12px;line-height:1.55}.actions{justify-content:flex-end;gap:10px;margin-top:14px}.actions button{flex:1}.actions .primary-btn{background:#d97706}
@media (max-width:480px){.candidate{padding:14px}.compare-header{display:none}.compare-row:not(.compare-header){grid-template-columns:1fr 1fr;grid-template-areas:"field status" "source candidate"}.compare-row>*{border-right:0}.field-name{grid-area:field;padding:10px;border-bottom:1px solid #eef2f7}.status{grid-area:status;justify-content:flex-end!important;padding:10px;border-bottom:1px solid #eef2f7}.source-value{grid-area:source}.candidate-value{grid-area:candidate;border-left:1px solid #e8edf3!important}.source-value,.candidate-value{display:flex;align-items:flex-start;flex-direction:column;gap:4px;padding:10px}.compare-row small{display:block;color:#94a3b8;font-size:10px}.raw-source>div{align-items:flex-start;flex-direction:column;gap:3px}}
</style>
