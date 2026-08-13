export function formatMoney(value) {
  if (value === null || value === undefined || value === '') return ''
  const amount = Number(value)
  if (!Number.isFinite(amount)) return ''
  if (Math.abs(amount) >= 100000000) return `${trim(amount / 100000000)}亿元`
  if (Math.abs(amount) >= 10000) return `${trim(amount / 10000)}万元`
  return `${trim(amount)}元`
}

function trim(value) { return Number(value.toFixed(2)).toString() }
export function ensureArray(value) { return Array.isArray(value) ? value : [] }
export function formatDate(value) {
  if (!value) return '时间未知'
  const date = new Date(value)
  const pad = n => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}
