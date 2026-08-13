import router from './router'

const API_BASE = (import.meta.env.VITE_API_BASE_URL || '/api/v1').replace(/\/$/, '')

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
    throw new Error('网络连接失败，请确认后端服务已启动')
  }
  const data = await response.json().catch(() => ({}))
  if (response.ok) return data
  if (response.status === 401 && options.auth !== false) {
    localStorage.removeItem('token')
    localStorage.removeItem('user')
    router.replace('/login')
  }
  throw new Error(data?.error?.message || `请求失败（${response.status}）`)
}

export function saveSession(data) {
  localStorage.setItem('token', data.token)
  localStorage.setItem('user', JSON.stringify(data.user || {}))
}

export function clearSession() {
  localStorage.removeItem('token')
  localStorage.removeItem('user')
}
