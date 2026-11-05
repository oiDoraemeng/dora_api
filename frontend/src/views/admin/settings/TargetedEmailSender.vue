<template>
  <section class="card">
    <header class="flex flex-col gap-3 border-b border-gray-100 px-6 py-4 dark:border-dark-700 lg:flex-row lg:items-start lg:justify-between">
      <div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">针对性发送邮件</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">按用户余额从低到高选择收件人，支持跨页勾选后批量投递。禁发设置对所有群发模板持续生效，可随时恢复。</p>
      </div>
      <span class="text-sm text-gray-500 dark:text-gray-400">已选择 <strong class="text-gray-900 dark:text-white">{{ selectedEmails.length }}</strong> 位用户</span>
    </header>

    <div class="space-y-6 p-6">
      <div class="min-w-0 space-y-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <label class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-200">
            <input v-model="selectCurrentPage" type="checkbox" :disabled="loadingDeliveryStatuses || deliveryStatusError || loadingUsers" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
            选择本页
          </label>
          <div class="flex w-full gap-2 sm:w-auto">
            <input v-model="searchQuery" type="search" class="input min-w-0 flex-1 sm:w-64" placeholder="搜索邮箱、用户名或备注" @keyup.enter="searchUsers" />
            <button type="button" class="btn btn-secondary btn-sm shrink-0" :disabled="loadingUsers" @click="searchUsers">搜索</button>
          </div>
          <div class="flex items-center gap-2">
            <select v-model.number="pageSize" class="input h-9 w-24 py-1 text-sm">
              <option :value="10">10 / 页</option>
              <option :value="20">20 / 页</option>
              <option :value="50">50 / 页</option>
            </select>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="loadingUsers" @click="loadUsers">刷新</button>
          </div>
        </div>

        <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
          <table class="w-full min-w-[1180px] text-left text-sm">
            <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-gray-400">
              <tr>
                <th class="w-12 px-4 py-3"></th>
                <th class="px-3 py-3 font-medium">用户</th>
                <th class="px-3 py-3 font-medium"><button type="button" class="inline-flex items-center gap-1 hover:text-gray-900 dark:hover:text-white" :aria-label="sortAriaLabel('balance', '余额')" @click="toggleSort('balance')">余额 <span class="text-[10px]">{{ sortIndicator('balance') }}</span></button></th>
                <th class="px-3 py-3 font-medium"><button type="button" class="inline-flex items-center gap-1 hover:text-gray-900 dark:hover:text-white" :aria-label="sortAriaLabel('usage', '用量')" @click="toggleSort('usage')">用量 <span class="text-[10px]">{{ sortIndicator('usage') }}</span></button></th>
                <th class="px-3 py-3 font-medium">状态</th>
                <th class="px-3 py-3 font-medium">备注</th>
                <th class="px-3 py-3 font-medium">邮件记录</th>
                <th class="px-3 py-3 font-medium">群发权限</th>
                <th class="px-3 py-3 font-medium"><button type="button" class="inline-flex items-center gap-1 hover:text-gray-900 dark:hover:text-white" :aria-label="sortAriaLabel('last_active_at', '最后活跃')" @click="toggleSort('last_active_at')">最后活跃 <span class="text-[10px]">{{ sortIndicator('last_active_at') }}</span></button></th>
                <th class="px-3 py-3 font-medium"><button type="button" class="inline-flex items-center gap-1 hover:text-gray-900 dark:hover:text-white" :aria-label="sortAriaLabel('last_used_at', '最后使用')" @click="toggleSort('last_used_at')">最后使用 <span class="text-[10px]">{{ sortIndicator('last_used_at') }}</span></button></th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-if="loadingUsers"><td colspan="10" class="px-4 py-10 text-center text-gray-500">正在加载用户...</td></tr>
              <tr v-else-if="users.length === 0"><td colspan="10" class="px-4 py-10 text-center text-gray-500">暂无匹配的用户</td></tr>
              <tr v-for="user in sortedUsers" :key="user.id" class="hover:bg-gray-50/70 dark:hover:bg-dark-800/50">
                <td class="px-4 py-3"><input v-model="selectedEmails" :value="user.email" type="checkbox" :disabled="loadingDeliveryStatuses || deliveryStatusError || savingPreferences.has(user.id) || deliveryStatus(user.email)?.sent_current || deliveryStatus(user.email)?.blocked" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 disabled:cursor-not-allowed disabled:opacity-50" /></td>
                <td class="px-3 py-3"><p class="font-medium text-gray-900 dark:text-white">{{ user.email }}</p><p class="mt-0.5 truncate text-xs text-gray-500 dark:text-gray-400">{{ user.username || `用户 #${user.id}` }}</p></td>
                <td class="px-3 py-3 font-medium text-gray-900 dark:text-white">{{ formatBalance(user.balance) }}</td>
                <td class="px-3 py-3 text-xs text-gray-600 dark:text-gray-300"><template v-if="usageStats[user.id]"><p>今日 {{ formatBalance(usageStats[user.id].today_actual_cost) }}</p><p class="mt-0.5 text-gray-500 dark:text-gray-400">累计 {{ formatBalance(usageStats[user.id].total_actual_cost) }}</p></template><span v-else class="text-gray-400">{{ loadingUsage ? '加载中...' : '-' }}</span></td>
                <td class="px-3 py-3"><span :class="user.status === 'active' ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-500'">{{ user.status === 'active' ? '正常' : '已禁用' }}</span></td>
                <td class="max-w-48 truncate px-3 py-3 text-xs text-gray-500 dark:text-gray-400" :title="user.notes || ''">{{ user.notes || '-' }}</td>
                <td class="px-3 py-3 text-xs"><p v-if="deliveryStatus(user.email)?.sent_current" class="font-medium text-amber-600 dark:text-amber-400">本模板已发送</p><p v-else class="text-gray-400">未发送当前模板</p><p v-if="deliveryStatus(user.email)?.last_sent_at" class="mt-0.5 text-gray-500 dark:text-gray-400">{{ formatDate(deliveryStatus(user.email)?.last_sent_at) }}</p></td>
                <td class="px-3 py-3 text-xs">
                  <p :class="deliveryStatus(user.email)?.blocked ? 'text-red-600 dark:text-red-400' : 'text-gray-500 dark:text-gray-400'">{{ loadingDeliveryStatuses ? '加载中...' : deliveryStatusError ? '加载失败' : deliveryStatus(user.email)?.blocked ? '已禁止群发' : '允许群发' }}</p>
                  <button type="button" class="mt-1 whitespace-nowrap underline disabled:opacity-50" :disabled="loadingDeliveryStatuses || deliveryStatusError || savingPreferences.has(user.id)" :aria-label="`${deliveryStatus(user.email)?.blocked ? '恢复' : '禁止'}向 ${user.email} 群发邮件`" @click="toggleUserEmailPreference(user)">{{ savingPreferences.has(user.id) ? '保存中...' : deliveryStatus(user.email)?.blocked ? '恢复群发' : '禁止群发' }}</button>
                </td>
                <td class="px-3 py-3 text-xs text-gray-500 dark:text-gray-400">{{ formatDate(user.last_active_at) }}</td>
                <td class="px-3 py-3 text-xs text-gray-500 dark:text-gray-400">{{ formatDate(user.last_used_at) }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <p v-if="deliveryStatusError" class="text-sm text-amber-600 dark:text-amber-400" role="status">无法加载群发权限，请刷新后再选择用户。</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">禁止后，已排队且尚未开始发送的邮件会自动跳过；正在发送中的邮件可能仍会送达。</p>

        <div class="flex flex-wrap items-center justify-between gap-3 text-sm text-gray-500 dark:text-gray-400">
          <span>共 {{ total }} 位用户，当前按 {{ currentSortLabel }}</span>
          <div class="flex items-center gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="page <= 1 || loadingUsers" @click="page--">上一页</button><span>第 {{ page }} / {{ totalPages }} 页</span><button type="button" class="btn btn-secondary btn-sm" :disabled="page >= totalPages || loadingUsers" @click="page++">下一页</button></div>
        </div>
      </div>

      <div class="min-w-0 space-y-4 border-t border-gray-100 pt-6 dark:border-dark-700">
        <div class="grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(22rem,0.9fr)]">
          <div class="space-y-4">
            <div class="flex items-center gap-2"><div class="grid flex-1 grid-cols-2 gap-2">
              <button v-for="item in templates" :key="item.key" type="button" class="rounded-md border px-3 py-2 text-sm font-medium transition-colors disabled:opacity-50" :disabled="loadingTemplates || templateLoadError" :class="templateKey === item.key ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-300' : 'border-gray-200 text-gray-600 hover:border-primary-300 dark:border-dark-600 dark:text-gray-300'" @click="applyTemplate(item.key)">{{ item.label }}</button>
            </div><button type="button" class="btn btn-secondary btn-sm shrink-0" :disabled="loadingTemplates || templateLoadError || savingTemplates" @click="saveTemplates">{{ savingTemplates ? '保存中...' : '保存模板' }}</button></div>
            <p v-if="templateLoadError" class="text-sm text-amber-600 dark:text-amber-400" role="status">模板加载失败，请重试后继续编辑。<button type="button" class="ml-2 underline" @click="loadTemplates">重新加载模板</button></p>
            <p v-else class="text-xs text-gray-500 dark:text-gray-400" role="status">{{ loadingTemplates ? '正在加载已保存的模板...' : templatesDirty ? '有未保存的修改' : hasSavedTemplates ? '模板已保存' : '当前使用默认模板' }}。修改后点击“保存模板”，四个模板的主题和 HTML 将一起保存，下次打开可继续修改使用。</p>
            <div><label class="input-label" for="targeted-email-subject">邮件主题</label><input id="targeted-email-subject" v-model="subject" class="input" maxlength="200" :disabled="loadingTemplates || templateLoadError" /></div>
            <div><label class="input-label" for="targeted-email-body">邮件 HTML</label><textarea id="targeted-email-body" v-model="body" rows="18" class="input resize-y font-mono text-sm leading-[1.75rem]" maxlength="50000" :disabled="loadingTemplates || templateLoadError"></textarea></div>
          </div>
          <div class="space-y-4">
            <div><label class="input-label" for="targeted-email-test">测试收件人</label><div class="flex gap-2"><input id="targeted-email-test" v-model="testRecipient" type="email" class="input" placeholder="name@example.com" /><button type="button" class="btn btn-secondary shrink-0" :disabled="sendingTest || jobActive || !canSendTest" @click="sendTest">{{ sendingTest ? '发送中' : '测试发送' }}</button></div></div>
            <div>
              <label class="input-label" for="targeted-email-interval">发送间隔（分钟 / 封）</label>
              <input id="targeted-email-interval" v-model.number="intervalMinutes" type="number" min="1" max="1440" step="1" class="input" :disabled="jobActive || sendingBatch" />
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">默认每 5 分钟发送一封，可设置 1–1440 分钟。首封在队列可用后发送，其余逐封间隔投递。</p>
              <p v-if="validInterval && selectedEmails.length" class="mt-1 text-xs text-gray-500 dark:text-gray-400">预计至少需要 {{ estimatedDuration }}（不含发送耗时、已有任务等待时间）。</p>
              <p v-if="selectedEmails.length > 500" class="mt-1 text-xs text-red-600">每次最多选择 500 位用户。</p>
            </div>
            <button type="button" class="btn btn-primary w-full" :disabled="sendingBatch || loadingJob || jobActive || !canSendBatch" @click="sendBatch">{{ sendingBatch ? '正在创建任务...' : jobActive ? '后台发送任务进行中' : `批量发送给 ${selectedEmails.length} 位用户` }}</button>
            <div v-if="jobLoadError" class="text-sm text-amber-600 dark:text-amber-400" role="status">进度暂时无法更新，正在自动重试。<button type="button" class="ml-2 underline" @click="loadJob">立即刷新</button></div>
            <div v-if="emailJob" class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700" aria-label="邮件发送进度" aria-live="polite">
              <div class="flex items-center justify-between gap-2"><p class="font-medium text-gray-900 dark:text-white">{{ jobStatusLabel }} · {{ progressPercent }}%</p><button v-if="emailJob.status === 'running'" type="button" class="btn btn-secondary btn-sm" :disabled="cancellingJob" @click="cancelJob">{{ cancellingJob ? '取消中...' : '取消任务' }}</button></div>
              <p class="break-all text-sm text-gray-600 dark:text-gray-300">{{ emailJob.subject }}</p>
              <div class="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700" role="progressbar" :aria-valuenow="progressPercent" :aria-valuemin="0" :aria-valuemax="100" aria-label="邮件处理进度"><div class="h-full bg-primary-500 transition-all" :style="{ width: `${progressPercent}%` }"></div></div>
              <p class="text-sm text-gray-600 dark:text-gray-300">已处理 {{ processedCount }} / {{ emailJob.total }}：成功 {{ emailJob.sent }}，失败 {{ emailJob.failed }}，跳过 {{ emailJob.skipped }}，待发送 {{ emailJob.pending }}<template v-if="emailJob.sending">，发送中 {{ emailJob.sending }}</template><template v-if="emailJob.cancelled">，已取消 {{ emailJob.cancelled }}</template></p>
              <p v-if="emailJob.status === 'running' && emailJob.pending" class="text-xs text-gray-500 dark:text-gray-400">每 {{ emailJob.interval_minutes }} 分钟一封，下一封预计 {{ formatDate(emailJob.next_send_at) }}</p>
              <p class="text-xs text-gray-500 dark:text-gray-400">任务在后台运行，关闭或刷新页面后仍会继续。取消时正在发送的邮件可能仍会完成。</p>
              <details v-if="emailJob.errors?.length" class="text-xs text-amber-600 dark:text-amber-400"><summary class="cursor-pointer">查看失败与跳过详情（最多 20 条）</summary><ul class="mt-2 space-y-1 break-all"><li v-for="(error, index) in emailJob.errors" :key="index">{{ error }}</li></ul></details>
            </div>
            <div class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700"><div class="border-b border-gray-100 bg-gray-50 px-4 py-3 text-sm font-medium text-gray-700 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-200">预览: {{ subject || '请输入主题' }}</div><iframe class="h-[34rem] w-full bg-white" sandbox="" :srcdoc="previewHTML" title="目标邮件预览"></iframe></div>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { adminAPI } from '@/api'
import { useAppStore } from '@/stores'
import type { AdminUser } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { BatchUserUsageStats } from '@/api/admin/dashboard'
import type { TargetedEmailJob, TargetedEmailDeliveryStatus, TargetedEmailTemplate } from '@/api/admin/settings'

type TemplateKey = 'thanks' | 'offer' | 'promotion' | 'notice'
type EmailTemplate = { key: TemplateKey; label: string; subject: string; body: string }

function createEmailTemplate(accent: string, title: string, content: string): string {
  return `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <style>
    body { margin: 0; padding: 24px; background: #f4f4f5; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: #18181b; }
    .container { max-width: 640px; margin: 0 auto; background: #ffffff; border-radius: 12px; overflow: hidden; box-shadow: 0 8px 30px rgba(15, 23, 42, 0.10); }
    .header { background: ${accent}; color: #ffffff; padding: 28px 32px; }
    .header h1 { margin: 0; font-size: 24px; line-height: 1.25; }
    .content { padding: 28px 32px; font-size: 15px; line-height: 1.75; }
    .content p { margin: 0 0 14px; }
    .footer { padding: 18px 32px; background: #fafafa; color: #a1a1aa; font-size: 12px; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header"><h1>${title}</h1></div>
    <div class="content">${content}</div>
    <div class="footer">此邮件由 Dora2API 发送，请勿直接回复。</div>
  </div>
</body>
</html>`
}

const templates = ref<EmailTemplate[]>([
  { key: 'thanks', label: '感谢邮件', subject: '感谢您一直以来的支持', body: createEmailTemplate('#4f46e5', '感谢您的支持', '<p>您好，</p><p>感谢您选择并使用 Dora2API。您的支持是我们持续改进的动力。</p><p>如有任何建议或需要帮助，欢迎随时联系我们。</p><p>祝您使用愉快！</p>') },
  { key: 'offer', label: '优惠邮件', subject: '专属优惠，限时领取', body: createEmailTemplate('#16a34a', '专属优惠，限时领取', '<p>您好，</p><p>我们为您准备了一份 Dora2API 专属优惠，欢迎前往充值页面查看并领取。</p><p>优惠数量有限，请及时使用。</p><p>感谢您的支持！</p>') },
  { key: 'promotion', label: '推广邮件', subject: '服务新功能与活动推荐', body: createEmailTemplate('#0891b2', '服务新功能与活动推荐', '<p>您好，</p><p>Dora2API 近期更新了服务能力，并准备了新的活动内容。欢迎登录平台了解详情。</p><p>期待继续为您提供服务！</p>') },
  { key: 'notice', label: '通知邮件', subject: 'Dora2API 服务通知', body: createEmailTemplate('#ea580c', 'Dora2API 服务通知', '<p>您好，</p><p>这里是一条来自 Dora2API 的服务通知。请登录平台查看相关信息并及时处理。</p><p>感谢您的理解与支持。</p>') },
])

const appStore = useAppStore()
const users = ref<AdminUser[]>([])
const selectedEmails = ref<string[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const loadingUsers = ref(false)
const loadingUsage = ref(false)
const searchQuery = ref('')
type SortKey = 'balance' | 'usage' | 'last_active_at' | 'last_used_at'
const sortKey = ref<SortKey>('balance')
const sortOrder = ref<'asc' | 'desc'>('asc')
const usageStats = ref<Record<string, BatchUserUsageStats>>({})
const deliveryStatuses = ref<Record<string, TargetedEmailDeliveryStatus>>({})
const loadingDeliveryStatuses = ref(true)
const deliveryStatusError = ref(false)
const savingPreferences = ref(new Set<number>())
let deliveryStatusRequestSequence = 0
let deliveryStatusTimer: ReturnType<typeof setTimeout> | undefined
let usageRequestSequence = 0
const templateKey = ref<TemplateKey>('thanks')
const subject = ref(templates.value[0].subject)
const body = ref(templates.value[0].body)
const savingTemplates = ref(false)
const loadingTemplates = ref(true)
const templateLoadError = ref(false)
const hasSavedTemplates = ref(false)
const savedTemplatesSnapshot = ref('')
const templatesDirty = computed(() => !loadingTemplates.value && !templateLoadError.value && templateSnapshot(currentTemplatePayload()) !== savedTemplatesSnapshot.value)
const testRecipient = ref('')
const sendingTest = ref(false)
const sendingBatch = ref(false)
const intervalMinutes = ref(5)
const emailJob = ref<TargetedEmailJob | null>(null)
const loadingJob = ref(true)
const jobLoadError = ref(false)
const cancellingJob = ref(false)
let jobPollTimer: ReturnType<typeof setTimeout> | undefined
let jobRequestSequence = 0
let unmounted = false
const jobActive = computed(() => emailJob.value?.status === 'running' || Boolean(emailJob.value?.sending))
const validInterval = computed(() => Number.isInteger(intervalMinutes.value) && intervalMinutes.value >= 1 && intervalMinutes.value <= 1440)
const processedCount = computed(() => emailJob.value ? emailJob.value.sent + emailJob.value.failed + emailJob.value.skipped + emailJob.value.cancelled : 0)
const progressPercent = computed(() => emailJob.value ? Math.round(processedCount.value / emailJob.value.total * 100) : 0)
const jobStatusLabel = computed(() => emailJob.value?.status === 'running' ? '正在发送' : emailJob.value?.status === 'cancelled' ? '任务已取消' : '发送完成')
const estimatedDuration = computed(() => {
  const minutes = Math.max(0, selectedEmails.value.length - 1) * intervalMinutes.value
  const hours = Math.floor(minutes / 60)
  return hours ? `${hours} 小时 ${minutes % 60} 分钟` : `${minutes} 分钟`
})

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))
const pageEmails = computed(() => loadingDeliveryStatuses.value || deliveryStatusError.value ? [] : users.value.filter((user) => !deliveryStatus(user.email)?.sent_current && !deliveryStatus(user.email)?.blocked && !savingPreferences.value.has(user.id)).map((user) => user.email))
const sortedUsers = computed(() => {
  if (sortKey.value !== 'usage') return users.value
  const multiplier = sortOrder.value === 'asc' ? 1 : -1
  return [...users.value].sort((left, right) => {
    const leftUsage = usageStats.value[left.id]?.total_actual_cost ?? 0
    const rightUsage = usageStats.value[right.id]?.total_actual_cost ?? 0
    return (leftUsage - rightUsage) * multiplier
  })
})
const currentSortLabel = computed(() => {
  const labels: Record<SortKey, string> = { balance: '余额', usage: '累计用量', last_active_at: '最后活跃时间', last_used_at: '最后使用时间' }
  return `${labels[sortKey.value]}${sortOrder.value === 'asc' ? '升序' : '降序'}`
})
const selectCurrentPage = computed({
  get: () => pageEmails.value.length > 0 && pageEmails.value.every((email) => selectedEmails.value.includes(email)),
  set: (checked: boolean) => {
    const selection = new Set(selectedEmails.value)
    pageEmails.value.forEach((email) => checked ? selection.add(email) : selection.delete(email))
    selectedEmails.value = [...selection]
  },
})
const canSendTest = computed(() => !loadingTemplates.value && !templateLoadError.value && isEmail(testRecipient.value) && Boolean(subject.value.trim()) && Boolean(body.value.trim()))
const canSendBatch = computed(() => !loadingTemplates.value && !templateLoadError.value && selectedEmails.value.length > 0 && selectedEmails.value.length <= 500 && validInterval.value && !jobLoadError.value && !loadingDeliveryStatuses.value && !deliveryStatusError.value && savingPreferences.value.size === 0 && Boolean(subject.value.trim()) && Boolean(body.value.trim()))
const previewHTML = computed(() => body.value)

function isEmail(value: string): boolean { return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value.trim()) }
function formatBalance(value: number): string { return `$${Number(value || 0).toFixed(2)}` }
function formatDate(value?: string | null): string { return value ? new Date(value).toLocaleString() : '-' }
function deliveryStatus(email: string): TargetedEmailDeliveryStatus | undefined { return deliveryStatuses.value[email.trim().toLowerCase()] }
function syncCurrentTemplate(): void {
  const item = templates.value.find((candidate) => candidate.key === templateKey.value)
  if (item) { item.subject = subject.value; item.body = body.value }
}
function selectTemplate(key: TemplateKey): void {
  const item = templates.value.find((candidate) => candidate.key === key)
  if (item) { templateKey.value = key; subject.value = item.subject; body.value = item.body }
}
function applyTemplate(key: TemplateKey): void {
  if (loadingTemplates.value || templateLoadError.value) return
  syncCurrentTemplate()
  selectTemplate(key)
}
function currentTemplatePayload(): TargetedEmailTemplate[] {
  return templates.value.map((item) => ({ key: item.key, subject: (item.key === templateKey.value ? subject.value : item.subject).trim(), body: (item.key === templateKey.value ? body.value : item.body).trim() }))
}
function templateSnapshot(items: TargetedEmailTemplate[]): string {
  return JSON.stringify([...items].sort((a, b) => a.key.localeCompare(b.key)).map(({ key, subject, body }) => ({ key, subject, body })))
}
async function saveTemplates(): Promise<void> {
  if (loadingTemplates.value || templateLoadError.value || savingTemplates.value) return
  const payload = currentTemplatePayload()
  if (payload.some((item) => !item.subject || !item.body)) { appStore.showError('请填写每个模板的主题和 HTML 正文'); return }
  savingTemplates.value = true
  try {
    const result = await adminAPI.settings.saveTargetedEmailTemplates(payload)
    if (unmounted) return
    savedTemplatesSnapshot.value = templateSnapshot(result.templates)
    hasSavedTemplates.value = true
    appStore.showSuccess('邮件模板已保存，下次打开可继续修改使用')
  } catch (error) { if (!unmounted) appStore.showError(extractApiErrorMessage(error)) }
  finally { savingTemplates.value = false }
}
async function loadTemplates(): Promise<void> {
  loadingTemplates.value = true
  templateLoadError.value = false
  try {
    const result = await adminAPI.settings.getTargetedEmailTemplates()
    if (unmounted) return
    if (result.templates.length) {
      const saved = new Map(result.templates.map((item) => [item.key, item]))
      if (saved.size !== templates.value.length || templates.value.some((item) => !saved.get(item.key)?.subject?.trim() || !saved.get(item.key)?.body?.trim())) throw new Error('已保存的模板数据不完整')
      templates.value = templates.value.map((item) => ({ ...item, ...saved.get(item.key)! }))
    }
    // Loading must not synchronize the old editor content into the saved template.
    selectTemplate(templateKey.value)
    savedTemplatesSnapshot.value = templateSnapshot(currentTemplatePayload())
    hasSavedTemplates.value = result.templates.length > 0
  } catch (error) {
    if (!unmounted) { templateLoadError.value = true; appStore.showError(extractApiErrorMessage(error)) }
  } finally { loadingTemplates.value = false }
}
function searchUsers(): void { if (page.value === 1) void loadUsers(); else page.value = 1 }
function sortIndicator(key: SortKey): string { return sortKey.value === key ? (sortOrder.value === 'asc' ? 'ASC' : 'DESC') : '' }
function sortAriaLabel(key: SortKey, label: string): string { return `${label}，${sortKey.value === key ? (sortOrder.value === 'asc' ? '当前升序，点击改为降序' : '当前降序，点击改为升序') : '点击排序'}` }
function toggleSort(key: SortKey): void {
  if (sortKey.value === key) sortOrder.value = sortOrder.value === 'asc' ? 'desc' : 'asc'
  else { sortKey.value = key; sortOrder.value = key === 'balance' ? 'asc' : 'desc' }
  if (page.value === 1) void loadUsers(); else page.value = 1
}

async function loadUsers(): Promise<void> {
  loadingUsers.value = true
  ++deliveryStatusRequestSequence
  loadingDeliveryStatuses.value = true
  const requestSequence = ++usageRequestSequence
  usageStats.value = {}
  loadingUsage.value = false
  try {
    const response = await adminAPI.users.list(page.value, pageSize.value, { search: searchQuery.value.trim() || undefined, sort_by: sortKey.value === 'usage' ? 'balance' : sortKey.value, sort_order: sortOrder.value, include_subscriptions: false })
    if (unmounted || requestSequence !== usageRequestSequence) return
    users.value = response.items
    total.value = response.total
		void loadDeliveryStatuses()
    if (response.items.length > 0) {
      loadingUsage.value = true
      try {
        const result = await adminAPI.dashboard.getBatchUsersUsage(response.items.map((user) => user.id))
        if (requestSequence === usageRequestSequence) usageStats.value = result.stats
      } catch (error) {
        if (requestSequence === usageRequestSequence) appStore.showError(extractApiErrorMessage(error))
      } finally {
        if (requestSequence === usageRequestSequence) loadingUsage.value = false
      }
    }
  } catch (error) { appStore.showError(extractApiErrorMessage(error)) } finally { loadingUsers.value = false }
}

async function loadDeliveryStatuses(): Promise<void> {
  const requestSequence = ++deliveryStatusRequestSequence
  if (users.value.length === 0) { deliveryStatuses.value = {}; loadingDeliveryStatuses.value = false; deliveryStatusError.value = false; return }
  loadingDeliveryStatuses.value = true
  const payload = requestPayload(users.value.map((user) => user.email))
  try {
    const result = await adminAPI.settings.getTargetedEmailDeliveryStatuses(payload)
    if (unmounted || requestSequence !== deliveryStatusRequestSequence) return
    deliveryStatuses.value = result.statuses
    deliveryStatusError.value = false
    selectedEmails.value = selectedEmails.value.filter((email) => !deliveryStatus(email)?.sent_current && !deliveryStatus(email)?.blocked)
  } catch (error) {
    if (!unmounted && requestSequence === deliveryStatusRequestSequence) { deliveryStatusError.value = true; appStore.showError(extractApiErrorMessage(error)) }
  } finally { if (requestSequence === deliveryStatusRequestSequence) loadingDeliveryStatuses.value = false }
}

async function toggleUserEmailPreference(user: AdminUser): Promise<void> {
  if (savingPreferences.value.has(user.id)) return
  const blocked = !deliveryStatus(user.email)?.blocked
  savingPreferences.value.add(user.id)
  try {
    await adminAPI.settings.setTargetedEmailUserPreference(user.id, blocked)
    ++deliveryStatusRequestSequence
    deliveryStatuses.value[user.email.trim().toLowerCase()] = { ...deliveryStatus(user.email), sent_current: deliveryStatus(user.email)?.sent_current ?? false, blocked }
    if (blocked) selectedEmails.value = selectedEmails.value.filter((email) => email !== user.email)
    appStore.showSuccess(blocked ? '已禁止该用户接收群发邮件，设置已保存' : '已恢复该用户接收群发邮件')
  } catch (error) { appStore.showError(extractApiErrorMessage(error)) }
  finally { savingPreferences.value.delete(user.id); if (!unmounted) void loadDeliveryStatuses() }
}

function requestPayload(recipients: string[]) { return { recipients, subject: subject.value.trim(), html: previewHTML.value } }
async function sendTest(): Promise<void> {
  sendingTest.value = true
  try { await adminAPI.settings.sendTargetedEmailTest(requestPayload([testRecipient.value.trim()])); appStore.showSuccess('测试邮件已发送') } catch (error) { appStore.showError(extractApiErrorMessage(error)) } finally { sendingTest.value = false }
}
async function sendBatch(): Promise<void> {
  if (!canSendBatch.value || jobActive.value || sendingBatch.value || loadingJob.value) return
  if (!window.confirm(`确认向 ${selectedEmails.value.length} 位用户发送当前邮件？每 ${intervalMinutes.value} 分钟发送一封，预计至少需要 ${estimatedDuration.value}。`)) return
  sendingBatch.value = true
  ++jobRequestSequence
  clearTimeout(jobPollTimer)
  try {
    emailJob.value = await adminAPI.settings.sendTargetedEmailBatch({ ...requestPayload([...selectedEmails.value]), interval_minutes: intervalMinutes.value })
    appStore.showSuccess('邮件发送任务已创建，将按设定间隔在后台发送')
    selectedEmails.value = []
  } catch (error) { appStore.showError(extractApiErrorMessage(error)) } finally { sendingBatch.value = false; void loadJob() }
}

async function loadJob(): Promise<void> {
  clearTimeout(jobPollTimer)
  const requestSequence = ++jobRequestSequence
  try {
    const { job } = await adminAPI.settings.getTargetedEmailJob()
    if (unmounted || requestSequence !== jobRequestSequence) return
    const previous = emailJob.value
    emailJob.value = job
    jobLoadError.value = false
    if (job && (previous?.sent !== job.sent || previous?.status !== job.status)) void loadDeliveryStatuses()
  } catch { if (!unmounted && requestSequence === jobRequestSequence) jobLoadError.value = true }
  finally {
    if (!unmounted && requestSequence === jobRequestSequence) {
      loadingJob.value = false
      jobPollTimer = setTimeout(() => void loadJob(), 5000)
    }
  }
}

async function cancelJob(): Promise<void> {
  if (!emailJob.value || !window.confirm('确认取消剩余邮件？正在发送的邮件可能仍会完成。')) return
  cancellingJob.value = true
  try {
    await adminAPI.settings.cancelTargetedEmailJob(emailJob.value.id)
    await loadJob()
  } catch (error) { appStore.showError(extractApiErrorMessage(error)) }
  finally { cancellingJob.value = false }
}

watch(pageSize, () => { page.value = 1 })
watch([page, pageSize], loadUsers)
watch([subject, body], () => {
  ++deliveryStatusRequestSequence
  loadingDeliveryStatuses.value = true
  clearTimeout(deliveryStatusTimer)
  deliveryStatusTimer = setTimeout(() => void loadDeliveryStatuses(), 300)
})
onMounted(() => {
  try {
    const saved = Number(localStorage.getItem('targeted-email-interval-minutes'))
    if (Number.isInteger(saved) && saved >= 1 && saved <= 1440) intervalMinutes.value = saved
  } catch { /* Storage can be unavailable in private browsing. */ }
  void loadTemplates(); void loadUsers(); void loadJob()
})
watch(intervalMinutes, (value) => {
  if (validInterval.value) { try { localStorage.setItem('targeted-email-interval-minutes', String(value)) } catch { /* Optional preference. */ } }
})
onUnmounted(() => { unmounted = true; clearTimeout(jobPollTimer); clearTimeout(deliveryStatusTimer) })
</script>
