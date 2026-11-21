<template>
  <BaseDialog :show="show" :title="t('admin.accounts.keywordRouting.title')" width="wide" :close-on-click-outside="!saving" @close="close">
    <div class="space-y-5">
      <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('admin.accounts.keywordRouting.description') }}</p>
      <p v-if="configError" role="alert" class="text-sm text-red-600">{{ configError }}</p>
      <div v-if="loading" class="py-4 text-center text-sm">{{ t('common.loading') }}</div>
      <template v-else-if="!configError">
        <form class="flex gap-2" @submit.prevent="addKeyword">
          <input v-model="newKeyword" class="input min-w-0 flex-1" :disabled="saving" :aria-label="t('admin.accounts.keywordRouting.newKeyword')" :placeholder="t('admin.accounts.keywordRouting.newKeyword')" />
          <button class="btn btn-secondary" :disabled="saving || !newKeyword.trim()">{{ t('common.add') }}</button>
        </form>
        <p v-if="validationError" role="alert" class="text-sm text-red-600">{{ validationError }}</p>
        <div class="flex max-h-52 flex-wrap gap-2 overflow-y-auto rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <span v-for="(keyword, index) in keywords" :key="keyword.toLowerCase()" class="inline-flex max-w-full items-center gap-2 rounded-lg bg-gray-100 px-3 py-1 text-sm dark:bg-dark-700">
            <span class="break-all">{{ keyword }}</span>
            <button type="button" :disabled="saving" :aria-label="`${t('common.delete')} ${keyword}`" class="shrink-0 text-gray-500 hover:text-red-500" @click="keywords.splice(index, 1)">×</button>
          </span>
          <span v-if="!keywords.length" class="text-sm text-amber-600 dark:text-amber-400">{{ t('admin.accounts.keywordRouting.emptyWarning') }}</span>
        </div>
        <div class="flex justify-end">
          <button class="btn btn-primary" :disabled="saving" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button>
        </div>
      </template>
      <div class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 class="font-medium">{{ t('admin.accounts.keywordRouting.today') }}</h3>
          <div class="flex items-center gap-2">
            <select v-model="selectedGroup" class="input max-w-52" :aria-label="t('admin.accounts.keywordRouting.group')" @change="loadStats">
              <option value="">{{ t('admin.accounts.keywordRouting.allGroups') }}</option>
              <option v-for="group in groups" :key="group.id" :value="String(group.id)">{{ group.name }}</option>
            </select>
            <button class="btn btn-secondary" :disabled="statsLoading" :aria-label="t('common.refresh')" @click="loadStats">{{ t('common.refresh') }}</button>
          </div>
        </div>
        <p v-if="statsError" role="status" class="text-sm text-amber-600">{{ t('admin.accounts.keywordRouting.statsUnavailable') }}</p>
        <template v-else-if="stats">
          <p class="text-xs text-gray-500">{{ stats.date }} · {{ stats.timezone }}</p>
          <div class="grid grid-cols-2 gap-3">
            <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-700"><div class="text-sm text-gray-500">{{ t('admin.accounts.keywordRouting.checks') }}</div><strong class="text-xl">{{ stats.checks.toLocaleString() }}</strong></div>
            <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-700"><div class="text-sm text-gray-500">{{ t('admin.accounts.keywordRouting.hits') }}</div><strong class="text-xl">{{ stats.hits.toLocaleString() }}</strong></div>
          </div>
          <p class="text-xs text-gray-500">{{ t('admin.accounts.keywordRouting.statsHint') }}</p>
          <div v-if="!selectedGroup && stats.groups.length" class="max-h-40 overflow-auto">
            <table class="w-full text-left text-sm"><thead><tr><th>{{ t('admin.accounts.keywordRouting.group') }}</th><th>{{ t('admin.accounts.keywordRouting.checks') }}</th><th>{{ t('admin.accounts.keywordRouting.hits') }}</th></tr></thead><tbody><tr v-for="group in stats.groups" :key="group.group_id"><td class="py-1">{{ groupName(group.group_id) }}</td><td>{{ group.checks.toLocaleString() }}</td><td>{{ group.hits.toLocaleString() }}</td></tr></tbody></table>
          </div>
          <div class="max-h-48 overflow-auto">
            <table class="w-full text-left text-sm"><thead><tr><th>{{ t('admin.accounts.keywordRouting.keyword') }}</th><th>{{ t('admin.accounts.keywordRouting.hits') }}</th></tr></thead><tbody><tr v-for="word in stats.keywords" :key="word.keyword"><td class="break-all py-1">{{ word.keyword }}</td><td>{{ word.hits.toLocaleString() }}</td></tr><tr v-if="!stats.keywords.length"><td colspan="2" class="py-2 text-gray-500">{{ t('common.noData') }}</td></tr></tbody></table>
          </div>
        </template>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { accountsAPI, type KeywordRoutingTodayStats } from '@/api/admin/accounts'
import { useAppStore } from '@/stores/app'

const props = defineProps<{ show: boolean; groups: { id: number; name: string }[] }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const appStore = useAppStore()
const keywords = ref<string[]>([])
const newKeyword = ref('')
const loading = ref(false)
const saving = ref(false)
const configError = ref('')
const validationError = ref('')
const selectedGroup = ref('')
const stats = ref<KeywordRoutingTodayStats | null>(null)
const statsLoading = ref(false)
const statsError = ref(false)
let statsController: AbortController | null = null
let generation = 0
function close() { if (!saving.value) emit('close') }
function groupName(id: number) { return props.groups.find(group => group.id === id)?.name ?? `#${id}` }
function addKeyword() {
  const word = newKeyword.value.trim()
  if (!word) return
  const duplicate = keywords.value.some(existing => existing.toLowerCase() === word.toLowerCase())
  if ([...word].length > 200 || (!duplicate && keywords.value.length >= 10000)) { validationError.value = t('admin.accounts.keywordRouting.limits'); return }
  if (!duplicate) keywords.value.push(word)
  newKeyword.value = ''
  validationError.value = ''
}
async function save() {
  if (newKeyword.value.trim()) { addKeyword(); if (validationError.value) return }
  saving.value = true
  try {
    keywords.value = (await accountsAPI.setKeywordRoutingConfig({ keywords: keywords.value })).keywords
    appStore.showSuccess(t('admin.accounts.keywordRouting.saved'))
  } catch { appStore.showError(t('admin.accounts.keywordRouting.saveFailed')) }
  finally { saving.value = false }
}
async function loadStats() {
  statsController?.abort()
  const controller = new AbortController()
  statsController = controller
  statsLoading.value = true
  statsError.value = false
  stats.value = null
  try { const result = await accountsAPI.getKeywordRoutingTodayStats(selectedGroup.value ? Number(selectedGroup.value) : undefined, controller.signal); if (!controller.signal.aborted) stats.value = result }
  catch { if (!controller.signal.aborted) statsError.value = true }
  finally { if (!controller.signal.aborted) statsLoading.value = false }
}
watch(() => props.show, async show => {
  const current = ++generation
  if (!show) { statsController?.abort(); return }
  loading.value = true
  configError.value = ''
  newKeyword.value = ''
  validationError.value = ''
  void loadStats()
  try { const config = await accountsAPI.getKeywordRoutingConfig(); if (current === generation) keywords.value = config.keywords }
  catch { if (current === generation) configError.value = t('admin.accounts.keywordRouting.loadFailed') }
  finally { if (current === generation) loading.value = false }
}, { immediate: true })
onBeforeUnmount(() => { generation++; statsController?.abort() })
</script>
