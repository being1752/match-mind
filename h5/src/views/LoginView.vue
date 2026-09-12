<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { request, saveSession } from '../api'
import { useToast } from '../composables/useToast'

const router = useRouter()
const toast = useToast()
const mode = ref('login')
const username = ref('')
const password = ref('')
const displayName = ref('')
const loading = ref(false)
const testUsername = import.meta.env.VITE_TEST_ACCOUNT_USERNAME || ''
const testPassword = import.meta.env.VITE_TEST_ACCOUNT_PASSWORD || ''

function fillTestAccount() {
  mode.value = 'login'
  username.value = testUsername
  password.value = testPassword
  toast.show('已填入测试账号')
}

async function submit() {
  if (username.value.trim().length < 3 || password.value.length < 8) return toast.show('用户名至少3位，密码至少8位')
  loading.value = true
  try {
    const data = await request(mode.value === 'login' ? '/auth/login' : '/auth/register', {
      method: 'POST', auth: false,
      data: { username: username.value.trim(), password: password.value, display_name: displayName.value.trim() },
    })
    saveSession(data)
    router.replace('/ingest')
  } catch (error) { toast.show(error.message) } finally { loading.value = false }
}
</script>

<template>
  <div class="login-page">
    <div class="brand">MatchMind</div>
    <div class="subtitle"></div>
    <section class="panel auth-panel">
      <div class="mode-row"><button :class="{active:mode==='login'}" @click="mode='login'">登录</button><button :class="{active:mode==='register'}" @click="mode='register'">注册</button></div>
      <label v-if="mode==='register'" class="field"><span>姓名</span><input v-model="displayName" placeholder="用于标记信息来源" /></label>
      <label class="field"><span>用户名</span><input v-model="username" autocomplete="username" placeholder="至少3个字符" /></label>
      <label class="field"><span>密码</span><input v-model="password" type="password" autocomplete="current-password" placeholder="至少8个字符" @keyup.enter="submit" /></label>
      <button v-if="testUsername && testPassword" class="test-account" type="button" @click="fillTestAccount">一键填入测试账号</button>
      <button class="primary-btn submit" :disabled="loading" @click="submit">{{ loading ? '请稍候…' : mode==='login' ? '登录' : '创建账号' }}</button>
    </section>
  </div>
</template>

<style scoped>
.login-page { min-height:100vh; padding:80px 24px 36px; background:linear-gradient(180deg,#eef4ff,#f7f9fc 55%); }
.brand { color:#172033; font-size:36px; font-weight:800; text-align:center; }
.subtitle { margin:8px 0 38px; color:#64748b; text-align:center; }
.auth-panel { max-width:440px; margin:0 auto; padding:24px; }
.mode-row { display:flex; gap:28px; margin-bottom:24px; border-bottom:1px solid #edf1f7; }
.mode-row button { padding:0 5px 13px; border:0; background:none; color:#64748b; }
.mode-row button.active { border-bottom:2px solid #0052d9; color:#0052d9; font-weight:600; }
.field { display:block; margin-bottom:19px; }
.field span { display:block; margin-bottom:8px; color:#334155; font-size:14px; }
.field input { width:100%; height:48px; padding:0 14px; border:1px solid #dce3ed; border-radius:9px; outline:none; }
.field input:focus { border-color:#0052d9; box-shadow:0 0 0 3px rgba(0,82,217,.08); }
.submit { width:100%; }
.test-account { width:100%; margin:-4px 0 14px; padding:9px; border:1px dashed #8eb5f5; border-radius:9px; background:#f4f8ff; color:#0052d9; }
</style>
