import router from './router'

const API_BASE = (import.meta.env.VITE_API_BASE_URL || '/api/v1').replace(/\/$/, '')

const SERVER_ERROR_MESSAGES = {
  database_unavailable: '数据服务暂时不可用，请稍后重试',
  request_timeout: '请求处理超时，请稍后重试',
  internal_error: '系统处理失败，请稍后重试',
}

export async function request(path, options = {}) {
  const headers = { 'Content-Type': 'application/json', ...(options.headers || {}) }
  if (options.auth !== false) headers.Authorization = `Bearer ${localStorage.getItem('token') || ''}`
  let response
  try {
    response = await fetch(`${API_BASE}${path}`, {
      method: options.method || 'GET',
      headers,
      body: options.data === undefined ? undefined : JSON.stringify(options.data),
    })
  } catch (_) {
    throw new Error('服务暂时不可用，请稍后重试')
  }

  const data = await response.json().catch(() => ({}))
  if (response.ok) return data
  if (response.status === 401 && options.auth !== false) {
    localStorage.removeItem('token')
    localStorage.removeItem('user')
    router.replace('/login')
  }

  const requestId = data?.error?.request_id || response.headers.get('X-Request-ID') || ''
  const message = response.status >= 500
    ? (SERVER_ERROR_MESSAGES[data?.error?.code] || '服务暂时不可用，请稍后重试')
    : (data?.error?.message || `请求失败（${response.status}）`)
  throw new Error(requestId ? `${message}（请求编号：${requestId}）` : message)
}

export function saveSession(data) {
  localStorage.setItem('token', data.token)
  localStorage.setItem('user', JSON.stringify(data.user || {}))
}

export function clearSession() {
  localStorage.removeItem('token')
  localStorage.removeItem('user')
}
