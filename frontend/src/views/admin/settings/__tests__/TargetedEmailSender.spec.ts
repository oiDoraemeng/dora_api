import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import TargetedEmailSender from '../TargetedEmailSender.vue'
import type { TargetedEmailJob, TargetedEmailTemplate } from '@/api/admin/settings'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), usage: vi.fn(), templates: vi.fn(), saveTemplates: vi.fn(), statuses: vi.fn(),
  getJob: vi.fn(), sendBatch: vi.fn(), cancelJob: vi.fn(), preference: vi.fn(), showSuccess: vi.fn(), showError: vi.fn(),
}))
vi.mock('@/api', () => ({ adminAPI: {
  users: { list: mocks.list }, dashboard: { getBatchUsersUsage: mocks.usage },
  settings: {
    getTargetedEmailTemplates: mocks.templates, getTargetedEmailDeliveryStatuses: mocks.statuses,
    saveTargetedEmailTemplates: mocks.saveTemplates,
    getTargetedEmailJob: mocks.getJob, sendTargetedEmailBatch: mocks.sendBatch, cancelTargetedEmailJob: mocks.cancelJob,
    setTargetedEmailUserPreference: mocks.preference,
  },
} }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))

const runningJob: TargetedEmailJob = {
  id: 10, subject: '持久化任务', interval_minutes: 5, total: 500, status: 'running',
  sent: 100, failed: 2, skipped: 3, pending: 395, sending: 0, cancelled: 0,
  next_send_at: '2026-11-04T12:05:00+08:00', created_at: '2026-11-04T12:00:00+08:00', errors: ['bad@example.com: rate limit'],
}
let wrapper: VueWrapper | undefined
function batchButton() { return wrapper!.findAll('button').find(button => /批量发送给|后台发送任务/.test(button.text()))! }

beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  localStorage.clear()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  mocks.list.mockResolvedValue({ items: [{ id: 1, email: 'a@example.com', balance: 1, status: 'active' }], total: 1 })
  mocks.usage.mockResolvedValue({ stats: {} })
  mocks.templates.mockResolvedValue({ templates: [] })
  mocks.saveTemplates.mockImplementation(async (templates) => ({ templates }))
  mocks.statuses.mockResolvedValue({ statuses: {} })
  mocks.getJob.mockResolvedValue({ job: null })
  mocks.sendBatch.mockResolvedValue(runningJob)
  mocks.cancelJob.mockResolvedValue(undefined)
  mocks.preference.mockResolvedValue(undefined)
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; vi.useRealTimers(); vi.restoreAllMocks() })

describe('targeted email background campaigns', () => {
  it('saves all four templates and restores editable subjects and HTML on the next visit', async () => {
    let stored: TargetedEmailTemplate[] = []
    mocks.templates.mockImplementation(async () => ({ templates: structuredClone(stored) }))
    mocks.saveTemplates.mockImplementation(async (templates: TargetedEmailTemplate[]) => {
      stored = structuredClone(templates)
      return { templates: structuredClone(stored) }
    })
    const slots = [
      { key: 'thanks', label: '感谢邮件' }, { key: 'offer', label: '优惠邮件' },
      { key: 'promotion', label: '推广邮件' }, { key: 'notice', label: '通知邮件' },
    ] as const
    wrapper = mount(TargetedEmailSender)
    await flushPromises()
    for (const slot of slots) {
      await wrapper.findAll('button').find(button => button.text() === slot.label)!.trigger('click')
      await wrapper.get('#targeted-email-subject').setValue(`已修改的${slot.label}`)
      await wrapper.get('#targeted-email-body').setValue(`<html><body>${slot.key} 自定义内容</body></html>`)
    }
    expect(wrapper.text()).toContain('有未保存的修改')
    await wrapper.findAll('button').find(button => button.text() === '保存模板')!.trigger('click')
    await flushPromises()
    expect(stored).toHaveLength(4)
    expect(wrapper.text()).not.toContain('有未保存的修改')
    expect(wrapper.text()).toContain('模板已保存')
    wrapper.unmount()
    wrapper = mount(TargetedEmailSender)
    await flushPromises()
    // In particular, loading must never replace the saved default slot with
    // the component's built-in subject and HTML.
    expect(wrapper.get('#targeted-email-subject').element).toHaveProperty('value', '已修改的感谢邮件')
    for (const slot of slots) {
      await wrapper.findAll('button').find(button => button.text() === slot.label)!.trigger('click')
      expect(wrapper.get('#targeted-email-subject').element).toHaveProperty('value', `已修改的${slot.label}`)
      expect(wrapper.get('#targeted-email-body').element).toHaveProperty('value', `<html><body>${slot.key} 自定义内容</body></html>`)
    }
    await wrapper.get('#targeted-email-subject').setValue('再次编辑通知邮件')
    await wrapper.findAll('button').find(button => button.text() === '保存模板')!.trigger('click')
    await flushPromises()
    wrapper.unmount()
    wrapper = mount(TargetedEmailSender)
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '通知邮件')!.trigger('click')
    expect(wrapper.get('#targeted-email-subject').element).toHaveProperty('value', '再次编辑通知邮件')
    expect(stored.find(item => item.key === 'thanks')?.subject).toBe('已修改的感谢邮件')
  })

  it('keeps unsaved edits after a failed save or an edit made while saving', async () => {
    wrapper = mount(TargetedEmailSender)
    await flushPromises()
    await wrapper.get('#targeted-email-subject').setValue('第一次修改')
    mocks.saveTemplates.mockRejectedValueOnce(new Error('save failed'))
    await wrapper.findAll('button').find(button => button.text() === '保存模板')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('有未保存的修改')
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    let completeSave!: (result: { templates: TargetedEmailTemplate[] }) => void
    let savingPayload!: TargetedEmailTemplate[]
    mocks.saveTemplates.mockImplementationOnce((templates: TargetedEmailTemplate[]) => {
      savingPayload = templates
      return new Promise(resolve => { completeSave = resolve })
    })
    await wrapper.findAll('button').find(button => button.text() === '保存模板')!.trigger('click')
    await wrapper.get('#targeted-email-subject').setValue('保存期间的新修改')
    completeSave({ templates: savingPayload })
    await flushPromises()
    expect(wrapper.get('#targeted-email-subject').element).toHaveProperty('value', '保存期间的新修改')
    expect(wrapper.text()).toContain('有未保存的修改')
  })

  it('blocks editing after a load failure and allows a safe retry', async () => {
    mocks.templates.mockRejectedValueOnce(new Error('load failed'))
    wrapper = mount(TargetedEmailSender)
    await flushPromises()
    expect(wrapper.text()).toContain('模板加载失败')
    expect(wrapper.get('#targeted-email-subject').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button').find(button => button.text() === '保存模板')!.attributes('disabled')).toBeDefined()
    expect(mocks.saveTemplates).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text() === '重新加载模板')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).not.toContain('模板加载失败')
    expect(wrapper.get('#targeted-email-subject').attributes('disabled')).toBeUndefined()
  })

  it('persists a ban, removes the selected user, and restores it after remount', async () => {
    wrapper = mount(TargetedEmailSender)
    await flushPromises()
    await wrapper.get('tbody input[type="checkbox"]').setValue(true)
    expect(wrapper.text()).toContain('已选择 1 位用户')
    mocks.statuses.mockResolvedValue({ statuses: { 'a@example.com': { blocked: true, sent_current: false } } })
    await wrapper.get('button[aria-label="禁止向 a@example.com 群发邮件"]').trigger('click')
    await flushPromises()
    expect(mocks.preference).toHaveBeenCalledWith(1, true)
    expect(wrapper.text()).toContain('已选择 0 位用户')
    expect(wrapper.get('tbody input[type="checkbox"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
    wrapper = mount(TargetedEmailSender)
    await flushPromises()
    expect(wrapper.text()).toContain('已禁止群发')
    expect(wrapper.get('tbody input[type="checkbox"]').attributes('disabled')).toBeDefined()
    // The policy can still be loaded and restored with an empty email draft.
    await wrapper.get('#targeted-email-subject').setValue('')
    await wrapper.get('#targeted-email-body').setValue('')
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    expect(wrapper.text()).toContain('已禁止群发')
    mocks.statuses.mockResolvedValue({ statuses: {} })
    await wrapper.get('button[aria-label="恢复向 a@example.com 群发邮件"]').trigger('click')
    await flushPromises()
    expect(mocks.preference).toHaveBeenLastCalledWith(1, false)
    expect(wrapper.get('tbody input[type="checkbox"]').attributes('disabled')).toBeUndefined()
  })

  it('excludes banned users from page selection and leaves a failed save unchanged', async () => {
    mocks.list.mockResolvedValue({ items: [
      { id: 1, email: 'a@example.com', balance: 1, status: 'active' },
      { id: 2, email: 'b@example.com', balance: 2, status: 'active' },
    ], total: 2 })
    mocks.statuses.mockResolvedValue({ statuses: { 'a@example.com': { blocked: true, sent_current: false } } })
    wrapper = mount(TargetedEmailSender)
    await flushPromises()
    await wrapper.get('label input[type="checkbox"]').setValue(true)
    expect(wrapper.text()).toContain('已选择 1 位用户')
    expect(wrapper.findAll('tbody input[type="checkbox"]')[0].element).toHaveProperty('checked', false)
    mocks.preference.mockRejectedValueOnce(new Error('save failed'))
    await wrapper.get('button[aria-label="恢复向 a@example.com 群发邮件"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('已禁止群发')
    expect(mocks.showError).toHaveBeenCalled()
    expect(wrapper.findAll('tbody input[type="checkbox"]')[0].attributes('disabled')).toBeDefined()
  })

  it('sends a configurable interval to the queue and shows progress instead of claiming delivery', async () => {
    wrapper = mount(TargetedEmailSender)
    await flushPromises()
    expect(wrapper.get('#targeted-email-interval').element).toHaveProperty('value', '5')
    await wrapper.get('tbody input[type="checkbox"]').setValue(true)
    await wrapper.get('#targeted-email-interval').setValue(10)
    mocks.getJob.mockResolvedValue({ job: runningJob })
    await batchButton().trigger('click')
    await flushPromises()
    expect(mocks.sendBatch).toHaveBeenCalledWith(expect.objectContaining({ recipients: ['a@example.com'], interval_minutes: 10 }))
    expect(window.confirm).toHaveBeenCalledWith(expect.stringContaining('预计至少需要 0 分钟'))
    expect(wrapper.text()).toContain('已处理 105 / 500')
    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('21')
    expect(batchButton().attributes('disabled')).toBeDefined()
    expect(mocks.showSuccess).toHaveBeenCalledWith(expect.stringContaining('任务已创建'))
  })

  it('recovers an existing job, polls completion, and stops polling after unmount', async () => {
    mocks.getJob.mockResolvedValue({ job: runningJob })
    wrapper = mount(TargetedEmailSender)
    await flushPromises()
    expect(wrapper.text()).toContain('持久化任务')
    expect(wrapper.text()).toContain('rate limit')
    expect(wrapper.get('#targeted-email-interval').attributes('disabled')).toBeDefined()
    mocks.getJob.mockResolvedValue({ job: { ...runningJob, status: 'completed', sent: 495, pending: 0 } })
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(wrapper.text()).toContain('发送完成 · 100%')
    const count = mocks.getJob.mock.calls.length
    wrapper.unmount(); wrapper = undefined
    await vi.advanceTimersByTimeAsync(10000)
    expect(mocks.getJob).toHaveBeenCalledTimes(count)
  })

  it('cancels only the active job and refreshes its outcome', async () => {
    mocks.getJob.mockResolvedValue({ job: runningJob })
    wrapper = mount(TargetedEmailSender)
    await flushPromises()
    mocks.getJob.mockResolvedValue({ job: { ...runningJob, status: 'cancelled', pending: 0, cancelled: 395 } })
    await wrapper.findAll('button').find(button => button.text() === '取消任务')!.trigger('click')
    await flushPromises()
    expect(mocks.cancelJob).toHaveBeenCalledWith(10)
    expect(wrapper.text()).toContain('任务已取消')
    expect(wrapper.text()).toContain('已取消 395')
  })

  it('rejects invalid intervals and retains progress if polling fails', async () => {
    wrapper = mount(TargetedEmailSender)
    await flushPromises()
    await wrapper.get('tbody input[type="checkbox"]').setValue(true)
    for (const invalid of [0, 1.5, 1441]) {
      await wrapper.get('#targeted-email-interval').setValue(invalid)
      expect(batchButton().attributes('disabled')).toBeDefined()
    }
    mocks.getJob.mockResolvedValueOnce({ job: runningJob })
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    mocks.getJob.mockRejectedValueOnce(new Error('network unavailable'))
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(wrapper.text()).toContain('已处理 105 / 500')
    expect(wrapper.text()).toContain('进度暂时无法更新')
  })
})
