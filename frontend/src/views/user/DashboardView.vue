<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loading && !stats" class="flex items-center justify-center py-12"><LoadingSpinner /></div>
      <UserDashboardStats v-if="stats" :stats="stats" :balance="user?.balance || 0" :is-simple="authStore.isSimpleMode" :platform-quotas="platformQuotas" :balance-low-notify-enabled="balanceLowNotifyEnabled" :system-default-threshold="systemDefaultThreshold" />
      <UserDashboardCharts v-model:startDate="startDate" v-model:endDate="endDate" v-model:granularity="granularity" :loading="loadingTrend || loadingModels" :loading-trend="loadingTrend" :loading-models="loadingModels" :trend="trendData" :models="modelStats" @dateRangeChange="loadCharts" @granularityChange="loadCharts" @refresh="refreshAll" />
      <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <div class="lg:col-span-2"><UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" /></div>
        <div class="lg:col-span-1"><UserDashboardQuickActions /></div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'; import { useAuthStore } from '@/stores/auth'; import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import AppLayout from '@/components/layout/AppLayout.vue'; import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'; import UserDashboardCharts from '@/components/user/dashboard/UserDashboardCharts.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'; import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import type { UsageLog, TrendDataPoint, ModelStat, PlatformQuotaItem } from '@/types'
import { getMyPlatformQuotas } from '@/api/user'
import { formatDateLocalInput } from '@/utils/format'
import { useAppStore } from '@/stores/app'

const authStore = useAuthStore(); const appStore = useAppStore(); const user = computed(() => authStore.user)
const stats = ref<UserStatsType | null>(null); const loading = ref(false); const loadingUsage = ref(false)
const loadingTrend = ref(false)
const loadingModels = ref(false)
let chartsRequest = 0
const trendData = ref<TrendDataPoint[]>([]); const modelStats = ref<ModelStat[]>([]); const recentUsage = ref<UsageLog[]>([])
const platformQuotas = ref<PlatformQuotaItem[] | null>(null)
const balanceLowNotifyEnabled = ref(false)
const systemDefaultThreshold = ref(0)

const startDate = ref(formatDateLocalInput(new Date(Date.now() - 6 * 86400000))); const endDate = ref(formatDateLocalInput(new Date())); const granularity = ref<'day' | 'hour'>('day')

const loadStats = async () => {
  loading.value = true
  try { stats.value = await usageAPI.getDashboardStats() }
  catch (error) { console.error('Failed to load dashboard stats:', error) }
  finally { loading.value = false }
}
const loadCharts = async () => {
  const request = ++chartsRequest
  const params = { start_date: startDate.value, end_date: endDate.value }
  loadingTrend.value = true
  loadingModels.value = true
  // Publish each chart independently and ignore responses for old date ranges.
  await Promise.allSettled([
    usageAPI.getDashboardTrend({ ...params, granularity: granularity.value })
      .then((response) => { if (request === chartsRequest) trendData.value = response.trend || [] })
      .catch((error) => console.error('Failed to load usage trend:', error))
      .finally(() => { if (request === chartsRequest) loadingTrend.value = false }),
    usageAPI.getDashboardModels(params)
      .then((response) => { if (request === chartsRequest) modelStats.value = response.models || [] })
      .catch((error) => console.error('Failed to load model stats:', error))
      .finally(() => { if (request === chartsRequest) loadingModels.value = false })
  ])
}
const loadRecent = async () => { loadingUsage.value = true; try { const res = await usageAPI.getByDateRange(startDate.value, endDate.value, undefined, 5, true); recentUsage.value = res.items } catch (error) { console.error('Failed to load recent usage:', error) } finally { loadingUsage.value = false } }
const loadPlatformQuotas = async () => { try { const data = await getMyPlatformQuotas(); platformQuotas.value = data.platform_quotas ?? [] } catch (error) { console.warn('Failed to load platform quotas:', error); platformQuotas.value = [] } }
const loadBalanceNotifySettings = async () => {
  try {
    const settings = await appStore.fetchPublicSettings()
    balanceLowNotifyEnabled.value = settings?.balance_low_notify_enabled ?? false
    systemDefaultThreshold.value = settings?.balance_low_notify_threshold ?? 0
  } catch (error) {
    console.warn('Failed to load balance notification settings:', error)
  }
}

const refreshAll = () => {
  void authStore.refreshUser().catch((error) => console.error('Failed to refresh user:', error))
  void loadStats(); void loadCharts(); void loadRecent(); void loadPlatformQuotas(); void loadBalanceNotifySettings()
}

onMounted(() => { refreshAll() })
onBeforeUnmount(() => { chartsRequest++ })
</script>
