import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import KeywordRoutingModal from '../KeywordRoutingModal.vue'
import { accountsAPI } from '@/api/admin/accounts'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() }) }))
vi.mock('@/api/admin/accounts', () => ({ accountsAPI: { getKeywordRoutingConfig: vi.fn(), setKeywordRoutingConfig: vi.fn(), getKeywordRoutingTodayStats: vi.fn() } }))

const summary = { date: '2026-11-09', timezone: 'Asia/Shanghai', checks: 12, hits: 3, groups: [{ group_id: 7, checks: 12, hits: 3 }], keywords: [{ keyword: '画图', hits: 3 }] }
function render() {
  return mount(KeywordRoutingModal, { props: { show: true, groups: [{ id: 7, name: 'Text group' }] }, global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } } })
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(accountsAPI.getKeywordRoutingConfig).mockResolvedValue({ keywords: ['画图', 'HELLO'] })
  vi.mocked(accountsAPI.getKeywordRoutingTodayStats).mockResolvedValue(summary)
  vi.mocked(accountsAPI.setKeywordRoutingConfig).mockImplementation(async config => config)
})
describe('KeywordRoutingModal', () => {
  it('adds and removes shared keywords, deduplicates case and saves', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('input').setValue(' hello ')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('input').setValue(' 新词 ')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('button[aria-label="common.delete 画图"]').trigger('click')
    const save = wrapper.findAll('button').find(button => button.text() === 'common.save')!
    await save.trigger('click')
    await flushPromises()
    expect(accountsAPI.setKeywordRoutingConfig).toHaveBeenCalledWith({ keywords: ['HELLO', '新词'] })
    expect(wrapper.text()).toContain('Text group')
    expect(wrapper.text()).toContain('Asia/Shanghai')
    wrapper.unmount()
  })
  it('shows unavailable statistics without disabling keyword editing', async () => {
    vi.mocked(accountsAPI.getKeywordRoutingTodayStats).mockRejectedValue(new Error('redis unavailable'))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.accounts.keywordRouting.statsUnavailable')
    expect(wrapper.get('input').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
  it('filters statistics by group and rejects overly long keywords', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('select').setValue('7')
    await flushPromises()
    expect(accountsAPI.getKeywordRoutingTodayStats).toHaveBeenLastCalledWith(7, expect.any(AbortSignal))
    await wrapper.get('input').setValue('词'.repeat(201))
    await wrapper.get('form').trigger('submit')
    expect(wrapper.text()).toContain('admin.accounts.keywordRouting.limits')
    expect(accountsAPI.setKeywordRoutingConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
