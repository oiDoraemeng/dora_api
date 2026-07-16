<template>
  <div ref="popoverRef" class="relative">
    <button
      type="button"
      class="flex h-7 w-7 items-center justify-center rounded-md text-gray-400 transition-colors hover:bg-emerald-50 hover:text-emerald-600 focus:outline-none focus:ring-2 focus:ring-emerald-500 dark:hover:bg-emerald-900/30 dark:hover:text-emerald-400"
      :title="t('profile.balanceNotify.title')"
      :aria-label="t('profile.balanceNotify.title')"
      :aria-expanded="open"
      @click="open = !open"
    >
      <Icon name="bell" size="sm" />
    </button>

    <Transition name="notify-popover">
      <div v-if="open" class="absolute left-0 top-full z-30 mt-2 w-[22rem] rounded-lg border border-gray-200 bg-white p-4 shadow-xl dark:border-dark-600 dark:bg-dark-800">
        <div class="flex items-start justify-between gap-3">
          <div>
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('profile.balanceNotify.title') }}</h3>
            <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('profile.balanceNotify.description') }}</p>
          </div>
          <label class="relative inline-flex shrink-0 cursor-pointer items-center">
            <input v-model="notifyEnabled" type="checkbox" class="sr-only peer" @change="handleToggle" />
            <span class="h-5 w-9 rounded-full bg-gray-200 after:absolute after:left-0.5 after:top-0.5 after:h-4 after:w-4 after:rounded-full after:border after:border-gray-300 after:bg-white after:transition-all peer-checked:bg-emerald-600 peer-checked:after:translate-x-4 peer-checked:after:border-white dark:bg-dark-600"></span>
          </label>
        </div>

        <div v-if="notifyEnabled" class="mt-4 space-y-4 border-t border-gray-100 pt-4 dark:border-dark-700">
          <div>
            <label class="input-label" for="dashboard-balance-threshold">{{ t('profile.balanceNotify.threshold') }}</label>
            <div class="flex items-center gap-2">
              <span class="text-sm text-gray-500">$</span>
              <input id="dashboard-balance-threshold" v-model.number="customThreshold" type="number" min="0" step="0.01" class="input min-w-0 flex-1" :placeholder="thresholdPlaceholder" />
              <button type="button" class="btn btn-primary btn-sm" :disabled="savingThreshold" @click="saveThreshold">{{ savingThreshold ? t('common.saving') : t('common.save') }}</button>
            </div>
          </div>

          <div>
            <label class="input-label">{{ t('profile.balanceNotify.extraEmails') }}</label>
            <div class="space-y-2">
              <div class="flex items-center gap-2 rounded-md bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:bg-emerald-900/20 dark:text-emerald-200">
                <Icon name="mail" size="sm" />
                <span class="min-w-0 flex-1 truncate">{{ primaryEmail }}</span>
                <span class="text-xs">默认</span>
              </div>
              <div v-for="entry in emailEntries" :key="entry.email" class="flex items-center gap-2 rounded-md bg-gray-50 px-3 py-2 text-sm dark:bg-dark-700">
                <Icon name="mail" size="sm" class="text-gray-400" />
                <span class="min-w-0 flex-1 truncate text-gray-700 dark:text-gray-200">{{ entry.email }}</span>
                <span v-if="entry.verified" class="text-xs text-emerald-600 dark:text-emerald-400">{{ t('profile.balanceNotify.verified') }}</span>
                <button type="button" class="text-xs text-red-500 hover:text-red-700" @click="removeEmail(entry.email)">{{ t('profile.balanceNotify.removeEmail') }}</button>
              </div>
              <div v-if="pendingEmail" class="rounded-md border border-amber-200 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-900/20">
                <p class="truncate text-sm text-amber-800 dark:text-amber-200">{{ pendingEmail }}</p>
                <div class="mt-2 flex gap-2">
                  <input v-model="verifyCode" type="text" maxlength="6" class="input min-w-0 flex-1" :placeholder="t('profile.balanceNotify.codePlaceholder')" />
                  <button type="button" class="btn btn-secondary btn-sm" :disabled="verifying || verifyCode.length !== 6" @click="verifyEmail">{{ verifying ? t('common.saving') : t('profile.balanceNotify.verify') }}</button>
                </div>
              </div>
            </div>
            <div v-if="!pendingEmail" class="mt-2 flex gap-2">
              <input v-model="newEmail" type="email" class="input min-w-0 flex-1" :placeholder="t('profile.balanceNotify.emailPlaceholder')" @keyup.enter="addEmail" />
              <button type="button" class="btn btn-secondary btn-sm" :disabled="sendingCode || !newEmail.trim()" @click="addEmail">{{ sendingCode ? t('common.loading') : t('common.add') }}</button>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { userAPI } from '@/api'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { NotifyEmailEntry } from '@/types'

const props = defineProps<{ systemDefaultThreshold: number }>()
const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const popoverRef = ref<HTMLElement | null>(null)
const open = ref(false)
const notifyEnabled = ref(authStore.user?.balance_notify_enabled ?? false)
const customThreshold = ref<number | null>(authStore.user?.balance_notify_threshold ?? null)
const emailEntries = ref<NotifyEmailEntry[]>([...(authStore.user?.balance_notify_extra_emails ?? [])])
const newEmail = ref('')
const pendingEmail = ref('')
const verifyCode = ref('')
const sendingCode = ref(false)
const verifying = ref(false)
const savingThreshold = ref(false)

const primaryEmail = computed(() => authStore.user?.email || '')
const thresholdPlaceholder = computed(() => props.systemDefaultThreshold > 0 ? `${t('profile.balanceNotify.systemDefault')} $${props.systemDefaultThreshold}` : t('profile.balanceNotify.thresholdPlaceholder'))

watch(() => authStore.user, (user) => {
  notifyEnabled.value = user?.balance_notify_enabled ?? false
  customThreshold.value = user?.balance_notify_threshold ?? null
  emailEntries.value = [...(user?.balance_notify_extra_emails ?? [])]
}, { deep: true })

function handleClickOutside(event: MouseEvent): void {
  if (popoverRef.value && !popoverRef.value.contains(event.target as Node)) open.value = false
}

onMounted(() => document.addEventListener('click', handleClickOutside))
onBeforeUnmount(() => document.removeEventListener('click', handleClickOutside))

async function handleToggle(): Promise<void> {
  if (notifyEnabled.value && !primaryEmail.value) {
    notifyEnabled.value = false
    appStore.showError(t('profile.balanceNotify.emailPlaceholder'))
    return
  }
  try {
    authStore.user = await userAPI.updateProfile({ balance_notify_enabled: notifyEnabled.value })
  } catch (error) {
    notifyEnabled.value = !notifyEnabled.value
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  }
}

async function saveThreshold(): Promise<void> {
  savingThreshold.value = true
  try {
    const threshold = customThreshold.value && customThreshold.value > 0 ? customThreshold.value : 0
    authStore.user = await userAPI.updateProfile({ balance_notify_threshold: threshold })
    appStore.showSuccess(t('common.saved'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally { savingThreshold.value = false }
}

async function addEmail(): Promise<void> {
  const email = newEmail.value.trim()
  if (!email) return
  if (email.toLowerCase() === primaryEmail.value.toLowerCase() || emailEntries.value.some((entry) => entry.email.toLowerCase() === email.toLowerCase())) {
    appStore.showError(t('profile.balanceNotify.emailDuplicate'))
    return
  }
  sendingCode.value = true
  try {
    await userAPI.sendNotifyEmailCode(email)
    pendingEmail.value = email
    newEmail.value = ''
    appStore.showSuccess(t('profile.balanceNotify.codeSent'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally { sendingCode.value = false }
}

async function verifyEmail(): Promise<void> {
  if (!pendingEmail.value || verifyCode.value.length !== 6) return
  verifying.value = true
  try {
    await userAPI.verifyNotifyEmail(pendingEmail.value, verifyCode.value)
    authStore.user = await userAPI.getProfile()
    pendingEmail.value = ''
    verifyCode.value = ''
    appStore.showSuccess(t('profile.balanceNotify.verifySuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally { verifying.value = false }
}

async function removeEmail(email: string): Promise<void> {
  try {
    await userAPI.removeNotifyEmail(email)
    authStore.user = await userAPI.getProfile()
    appStore.showSuccess(t('profile.balanceNotify.removeSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  }
}
</script>

<style scoped>
.notify-popover-enter-active, .notify-popover-leave-active { transition: opacity 150ms ease, transform 150ms ease; }
.notify-popover-enter-from, .notify-popover-leave-to { opacity: 0; transform: translateY(-4px); }
</style>
