<template>
  <div class="ck-auth">
    <div class="ck-auth__box">
      <div class="ck-auth__brand">Chonk Pilot</div>

      <!-- 步骤 1：登录 / 注册（61 §4.6：login-in / login-register；**前端不接触令牌**） -->
      <template v-if="step === 'creds'">
        <div class="ck-auth__title">
          {{ mode === 'register' ? $t('common.auth_register_title') : $t('common.auth_login_title') }}
        </div>
        <Input v-model="username" :placeholder="$t('common.auth_username')" :disabled="busy" />
        <Input
          v-model="password"
          type="password"
          :placeholder="$t('common.auth_password')"
          :disabled="busy"
          @keyup.enter="submitCreds"
        />
        <div v-if="errKey" class="ck-auth__error">{{ $t(errKey) }}</div>
        <Button class="ck-auth__submit" type="primary" :loading="busy" @click="submitCreds">
          {{ mode === 'register' ? $t('common.auth_register') : $t('common.auth_login') }}
        </Button>
        <div class="ck-auth__switch" @click="toggleMode">
          {{ mode === 'register' ? $t('common.auth_switch_to_login') : $t('common.auth_switch_to_register') }}
        </div>
      </template>

      <!-- 步骤 2：选目录（61 §4.6「流程」：登录 → 选目录 → instance-claim） -->
      <template v-else>
        <div class="ck-auth__title">{{ $t('common.auth_dir_title') }}</div>
        <div class="ck-auth__hint">{{ $t('common.auth_dir_hint') }}</div>
        <div class="ck-auth__dir">
          <Input
            v-model="workDir"
            :placeholder="$t('common.auth_dir_placeholder')"
            :disabled="busy"
            @keyup.enter="submitDir"
          />
          <!-- 目录选择器仅 GUI/native 形态可用（browser 形态无 native 对话框，见 19 §3.3） -->
          <Button v-if="!isBrowser" :title="$t('common.dir_picker_title')" @click="pickDir">
            {{ $t('common.select') }}
          </Button>
        </div>
        <div v-if="errKey" class="ck-auth__error">{{ $t(errKey) }}</div>
        <Button class="ck-auth__submit" type="primary" :loading="busy" @click="submitDir">
          {{ $t('common.auth_enter') }}
        </Button>
        <div class="ck-auth__switch" @click="switchAccount">{{ $t('common.auth_switch_account') }}</div>
      </template>
    </div>
  </div>
</template>

<script setup>
/**
 * 认证视图（61-消息一览 §4.6 认证域；阶段 2b-2）——**已认证前的唯一入口**。
 *
 * 两步骤（对应 61 §4.6「流程」：`登录 → 选目录 → instance-claim`）：
 *   1. `creds`：登录 / 注册（`login-in` / `login-register`；令牌由入口承载，前端不接触）；
 *   2. `dir`：选择/输入 `work_dir` → `instance-claim {user?, work_dir?}`（`useInstanceClaim`）；
 *      成功后 emit `ready` → App 挂载主界面。
 *
 * 首次挂载的起始步骤由 `initialStep` 决定（App 按首屏注入分派）：未认证 → `creds`；
 * 已认证（cookie / 免登录令牌有效，如浏览器刷新）→ 直接 `dir`（**不再要求凭证**）。
 *
 * 错误分派（61 §4.6）：
 *   - `login-failed` → 提示账号或密码错；`login-username-taken` → 提示重名；
 *   - `instance-unauthorized` → **回登录视图**（`markSignedOut()`）；
 *   - `instance-forbidden` → 提示无权访问该目录，**不回登录**（改目录再试）。
 *
 * 状态逻辑一律走 composable（`useAuth` / `useInstanceClaim` / `useDirPicker`）；
 * 交互走 `mq.emit`（见各 composable 内部），不直调 `window.go.*`。
 */
import { ref } from 'vue'
import { Button, Input } from '../../components/ui'
import { useAuth, LOGIN_FAILED, LOGIN_USERNAME_TAKEN } from '../../composables/useAuth.js'
import { useDirPicker } from '../../composables/useDirPicker.js'
import { useInstanceClaim } from '../../composables/useInstanceClaim.js'
import { isBrowserForm } from '../../utils/runtimeForm.js'

const props = defineProps({
  /** 起始步骤：'creds'（未认证）| 'dir'（已认证，只需选目录）。 */
  initialStep: { type: String, default: 'creds' },
})
const emit = defineEmits(['ready'])

const { busy, errorCode, login, register, signOut, markSignedOut } = useAuth()
const { claim, errorCode: claimErrorCode } = useInstanceClaim()
const { pickDir: pickDirNative } = useDirPicker()

const mode = ref('login') // login | register
const step = ref(props.initialStep === 'dir' ? 'dir' : 'creds')
const username = ref('')
const password = ref('')
const workDir = ref('')
const errKey = ref('')

const isBrowser = isBrowserForm()

/** 登录/注册错误码 → 文案 key（未知码原样带出 = 通用失败，不静默）。 */
function credsErrKey(code) {
  if (code === LOGIN_FAILED) return 'common.auth_err_login_failed'
  if (code === LOGIN_USERNAME_TAKEN) return 'common.auth_err_username_taken'
  return 'common.auth_err_unknown'
}

function toggleMode() {
  mode.value = mode.value === 'register' ? 'login' : 'register'
  errKey.value = ''
}

async function submitCreds() {
  const name = username.value.trim()
  if (!name || !password.value) {
    errKey.value = 'common.input_required'
    return
  }
  errKey.value = ''
  const ok = mode.value === 'register' ? await register(name, password.value) : await login(name, password.value)
  if (!ok) {
    errKey.value = credsErrKey(errorCode.value)
    return
  }
  password.value = ''
  step.value = 'dir' // 登录成功 → 选目录（61 §4.6 流程）
}

/** 目录选择（GUI/native）：复用既有 gui.dir.open-dialog（browser 形态无 native 选择器）。 */
async function pickDir() {
  try {
    const picked = await pickDirNative()
    if (picked) workDir.value = picked
  } catch (e) {
    console.warn('[auth] pick dir failed:', e)
  }
}

async function submitDir() {
  const dir = workDir.value.trim()
  if (!dir) {
    errKey.value = 'common.input_required'
    return
  }
  errKey.value = ''
  const ok = await claim({ user: username.value.trim() || undefined, work_dir: dir })
  if (ok) {
    emit('ready')
    return
  }
  const code = claimErrorCode.value
  if (code === 'instance-unauthorized') {
    // 令牌失效 / 未登录 → 回登录视图（61 §4.6：跳登录）
    markSignedOut()
    step.value = 'creds'
    errKey.value = 'common.auth_err_unauthorized'
    return
  }
  // instance-forbidden（无权访问该目录）→ **不跳登录**，提示换目录；其它码 → 通用失败提示
  errKey.value = code === 'instance-forbidden' ? 'common.auth_err_forbidden' : 'common.auth_err_claim_failed'
}

/** 换账号（dir 步骤）：登出（清令牌）→ 回登录视图。 */
async function switchAccount() {
  await signOut()
  step.value = 'creds'
  errKey.value = ''
}
</script>

<style scoped>
.ck-auth {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100vh;
  background: var(--bg-primary, #1e1e1e);
}
.ck-auth__box {
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: 360px;
  max-width: calc(100vw - 32px);
  padding: 24px;
  border-radius: 8px;
  background: var(--bg-secondary, #252526);
  color: var(--text-primary, #ddd);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.25);
}
.ck-auth__brand {
  font-size: 16px;
  font-weight: 600;
  text-align: center;
}
.ck-auth__title {
  font-size: var(--font-size-sm, 13px);
  color: var(--text-muted, #999);
  text-align: center;
}
.ck-auth__hint {
  font-size: 12px;
  line-height: 1.5;
  color: var(--text-muted, #999);
}
.ck-auth__dir {
  display: flex;
  align-items: center;
  gap: 6px;
}
.ck-auth__error {
  font-size: 12px;
  line-height: 1.5;
  color: var(--danger, #d9534f);
}
.ck-auth__submit {
  width: 100%;
}
.ck-auth__switch {
  font-size: 12px;
  color: var(--accent, #409eff);
  text-align: center;
  cursor: pointer;
}
</style>
