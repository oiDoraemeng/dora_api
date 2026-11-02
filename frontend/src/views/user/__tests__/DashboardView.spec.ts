import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import DashboardView from '../DashboardView.vue'

const { stats, trend, models, recent, refreshUser } = vi.hoisted(() => ({
  stats: vi.fn(), trend: vi.fn(), models: vi.fn(), recent: vi.fn(), refreshUser: vi.fn()
}))
vi.mock('@/api/usage', () => ({ usageAPI: {
  getDashboardStats: stats, getDashboardTrend: trend,
  getDashboardModels: models, getByDateRange: recent
} }))
vi.mock('@/api/user', () => ({ getMyPlatformQuotas: vi.fn().mockResolvedValue({}) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { balance: 10 }, isSimpleMode: false, refreshUser }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ fetchPublicSettings: vi.fn().mockResolvedValue({}) }) }))

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

const mountView = () => shallowMount(DashboardView, {
  global: { stubs: { AppLayout: { template: '<main><slot /></main>' } } }
})

describe('user dashboard independent loading', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    stats.mockResolvedValue({ total_requests: 1 })
    trend.mockResolvedValue({ trend: [] })
    models.mockResolvedValue({ models: [] })
    recent.mockResolvedValue({ items: [] })
    refreshUser.mockResolvedValue(undefined)
  })

  it('renders other modules while summary and user refresh are pending', async () => {
    stats.mockReturnValue(deferred().promise)
    refreshUser.mockReturnValue(deferred().promise)
    const wrapper = mountView()
    await flushPromises()
    expect(stats).toHaveBeenCalledOnce()
    expect(wrapper.findComponent({ name: 'UserDashboardQuickActions' }).exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'UserDashboardRecentUsage' }).props('loading')).toBe(false)
    expect(recent).toHaveBeenCalledWith(expect.any(String), expect.any(String), undefined, 5, true)
    wrapper.unmount()
  })

  it('publishes the trend before models and discards an old date range response', async () => {
    const oldModels = deferred<{ models: unknown[] }>()
    models.mockReturnValueOnce(oldModels.promise)
    trend.mockResolvedValueOnce({ trend: [{ date: 'new' }] })
    const wrapper = mountView()
    await flushPromises()
    const charts = wrapper.findComponent({ name: 'UserDashboardCharts' })
    expect(charts.props('loadingTrend')).toBe(false)
    expect(charts.props('loadingModels')).toBe(true)
    expect(charts.props('trend')).toEqual([{ date: 'new' }])
    models.mockResolvedValueOnce({ models: [{ model: 'latest' }] })
    charts.vm.$emit('dateRangeChange')
    await flushPromises()
    oldModels.resolve({ models: [{ model: 'old' }] })
    await flushPromises()
    expect(charts.props('models')).toEqual([{ model: 'latest' }])
    expect(charts.props('loadingModels')).toBe(false)
    wrapper.unmount()
  })
})
