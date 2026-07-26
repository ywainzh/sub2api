import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  getAllIncludingInactive: vi.fn(),
  getModelsListCandidates: vi.fn(),
  list: vi.fn(),
  runStatusCheck: vi.fn(),
  deleteStatusCheckAccounts: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      getAllIncludingInactive: api.getAllIncludingInactive,
      getModelsListCandidates: api.getModelsListCandidates
    },
    accounts: {
      list: api.list,
      runStatusCheck: api.runStatusCheck,
      deleteStatusCheckAccounts: api.deleteStatusCheckAccounts
    }
  }
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(undefined) })
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: api.showSuccess,
    showWarning: api.showWarning,
    showError: api.showError
  })
}))

vi.mock('vue-router', () => ({
  onBeforeRouteLeave: vi.fn()
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({
    global: {
      locale: { value: 'en' },
      t: (key: string) => key,
      setLocaleMessage: vi.fn()
    },
    install: vi.fn()
  }),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params ? `${key} ${JSON.stringify(params)}` : key
  })
}))

import AccountStatusCheckView from '../AccountStatusCheckView.vue'

function mountView() {
  return mount(AccountStatusCheckView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        ConfirmDialog: {
          props: ['show', 'title', 'message', 'confirmText'],
          emits: ['confirm', 'cancel'],
          template:
            '<div v-if="show" data-test="clear-dialog"><h2>{{ title }}</h2><p>{{ message }}</p><slot /><button data-test="confirm-clear" @click="$emit(\'confirm\')">{{ confirmText }}</button></div>'
        },
        Icon: true
      }
    }
  })
}

describe('AccountStatusCheckView', () => {
  beforeEach(() => {
    api.getAllIncludingInactive.mockReset().mockResolvedValue([
      { id: 2, name: 'OpenAI disabled', platform: 'openai', status: 'inactive' },
      { id: 1, name: 'OpenAI primary', platform: 'openai', status: 'active' },
      { id: 9, name: 'Claude', platform: 'anthropic', status: 'active' }
    ])
    api.getModelsListCandidates.mockReset().mockResolvedValue(['gpt-5.4', 'gpt-5.5'])
    api.list.mockReset().mockResolvedValue({
      items: [
        {
          credentials: {
            model_mapping: { 'gpt-5.3': 'gpt-5.3-codex' },
            compact_model_mapping: { 'gpt-5.4': 'gpt-5.4-mini' }
          }
        }
      ],
      total: 1,
      page: 1,
      page_size: 1000,
      total_pages: 1
    })
    api.runStatusCheck.mockReset().mockResolvedValue(undefined)
    api.deleteStatusCheckAccounts.mockReset().mockResolvedValue({
      requested: 0,
      deleted: 0,
      deleted_ids: [],
      failed: 0,
      failures: []
    })
    api.showSuccess.mockReset()
    api.showWarning.mockReset()
    api.showError.mockReset()
  })

  it('defaults to gpt-5.5 and the regular test mode while loading OpenAI groups', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(api.getModelsListCandidates).toHaveBeenCalledWith(2, 'openai')
    expect(api.list).toHaveBeenCalledWith(1, 1000, { group: '2', lite: 'false' })
    expect((wrapper.vm as unknown as { selectedModelId: string }).selectedModelId).toBe('gpt-5.5')
    expect((wrapper.vm as unknown as { selectedMode: string }).selectedMode).toBe('default')
    expect(wrapper.findAll('.input-hint')).toHaveLength(0)
    expect(wrapper.get('[role="img"]').text()).toBe('?')
    expect((wrapper.vm as unknown as { modelOptions: Array<{ value: string }> }).modelOptions.map((item) => item.value)).toEqual([
      'gpt-5.5',
      'gpt-5.3',
      'gpt-5.3-codex',
      'gpt-5.4',
      'gpt-5.4-mini'
    ])
  })

  it('renders the complete 403 diagnostic and can stop an active request', async () => {
    let capturedSignal: AbortSignal | undefined
    api.runStatusCheck.mockImplementation(async (_request, onEvent, signal?: AbortSignal) => {
      capturedSignal = signal
      onEvent({
        type: 'batch_start',
        total: 1,
        stats: { total: 1, completed: 0, normal: 0, unauthorized: 0, quota_exhausted: 0, forbidden: 0, other_error: 0 }
      })
      onEvent({
        type: 'account_result',
        account_id: 11,
        account_name: 'codex-11',
        category: 'forbidden',
        http_status: 403,
        error: 'Access forbidden (403): Agent runtime has been deleted. | consecutive_403=3/3',
        stats: { total: 1, completed: 1, normal: 0, unauthorized: 0, quota_exhausted: 0, forbidden: 1, other_error: 0 }
      })
      await new Promise<void>((resolve) => signal?.addEventListener('abort', () => resolve(), { once: true }))
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('consecutive_403=3/3')

    await wrapper.get('.btn-danger').trigger('click')
    await flushPromises()
    expect(capturedSignal?.aborted).toBe(true)
    expect((wrapper.vm as unknown as { runState: string }).runState).toBe('stopped')
  })

  it('wraps streamed response chunks into a timestamped terminal response block', async () => {
    api.runStatusCheck.mockImplementation(async (_request, onEvent) => {
      onEvent({
        type: 'batch_start',
        total: 1,
        stats: { total: 1, completed: 0, normal: 0, unauthorized: 0, quota_exhausted: 0, forbidden: 0, other_error: 0 }
      })
      for (const text of ['Hello', ' world', '!']) {
        onEvent({ type: 'account_log', account_id: 11, account_name: 'codex-11', log_type: 'content', text })
      }
      onEvent({ type: 'account_log', account_id: 11, account_name: 'codex-11', log_type: 'test_complete', text: 'done' })
      onEvent({
        type: 'account_result',
        account_id: 11,
        account_name: 'codex-11',
        category: 'normal',
        http_status: 200,
        stats: { total: 1, completed: 1, normal: 1, unauthorized: 0, quota_exhausted: 0, forbidden: 0, other_error: 0 }
      })
    })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()

    const terminal = wrapper.get('[role="log"]').text()
    expect(terminal).toMatch(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} INFO/)
    expect(terminal).toContain('admin.accounts.statusCheck.log.response')
    expect(terminal).toContain('Hello world!')
  })

  it('offers cleanup only for 401, 403, and other errors after a completed check', async () => {
    api.runStatusCheck.mockImplementation(async (_request, onEvent) => {
      onEvent({
        type: 'batch_start',
        group_id: 2,
        group_name: 'OpenAI disabled',
        model_id: 'gpt-5.5',
        mode: 'default',
        total: 4,
        stats: { total: 4, completed: 0, normal: 0, unauthorized: 0, quota_exhausted: 0, forbidden: 0, other_error: 0 }
      })
      const results = [
        { account_id: 11, account_name: 'unauthorized', category: 'unauthorized', http_status: 401 },
        { account_id: 12, account_name: 'quota', category: 'quota_exhausted', http_status: 429 },
        { account_id: 13, account_name: 'forbidden', category: 'forbidden', http_status: 403 },
        { account_id: 14, account_name: 'network', category: 'other_error' }
      ] as const
      results.forEach((result, index) => {
        onEvent({
          type: 'account_result',
          ...result,
          stats: {
            total: 4,
            completed: index + 1,
            normal: 0,
            unauthorized: index >= 0 ? 1 : 0,
            quota_exhausted: index >= 1 ? 1 : 0,
            forbidden: index >= 2 ? 1 : 0,
            other_error: index >= 3 ? 1 : 0
          }
        })
      })
      onEvent({
        type: 'batch_complete',
        group_id: 2,
        completed: 4,
        total: 4,
        stats: { total: 4, completed: 4, normal: 0, unauthorized: 1, quota_exhausted: 1, forbidden: 1, other_error: 1 }
      })
    })

    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-test="clear-unauthorized"]').attributes()).toHaveProperty('disabled')

    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()

    expect(wrapper.findAll('[data-test^="clear-"]')).toHaveLength(3)
    expect(wrapper.find('[data-test="clear-quota-exhausted"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="clear-unauthorized"]').attributes()).not.toHaveProperty('disabled')
    expect(wrapper.get('[data-test="clear-forbidden"]').attributes()).not.toHaveProperty('disabled')
    expect(wrapper.get('[data-test="clear-other-error"]').attributes()).not.toHaveProperty('disabled')
  })

  it('confirms permanent deletion and keeps failed accounts in the current result', async () => {
    api.runStatusCheck.mockImplementation(async (_request, onEvent) => {
      onEvent({
        type: 'batch_start',
        group_id: 2,
        group_name: 'OpenAI disabled',
        model_id: 'gpt-5.5',
        mode: 'default',
        total: 2,
        stats: { total: 2, completed: 0, normal: 0, unauthorized: 0, quota_exhausted: 0, forbidden: 0, other_error: 0 }
      })
      onEvent({ type: 'account_result', account_id: 21, account_name: 'expired-21', category: 'unauthorized' })
      onEvent({ type: 'account_result', account_id: 22, account_name: 'expired-22', category: 'unauthorized' })
      onEvent({
        type: 'batch_complete',
        group_id: 2,
        completed: 2,
        total: 2,
        stats: { total: 2, completed: 2, normal: 0, unauthorized: 2, quota_exhausted: 0, forbidden: 0, other_error: 0 }
      })
    })
    api.deleteStatusCheckAccounts.mockResolvedValue({
      requested: 2,
      deleted: 1,
      deleted_ids: [21],
      failed: 1,
      failures: [{ account_id: 22, code: 'delete_failed', message: 'database busy' }]
    })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="clear-unauthorized"]').trigger('click')

    expect(wrapper.get('[data-test="clear-dialog"]').text()).toContain('expired-21')
    expect(wrapper.get('[data-test="clear-dialog"]').text()).toContain('expired-22')
    expect(wrapper.get('[data-test="clear-dialog"]').text()).toContain('gpt-5.5')

    await wrapper.get('[data-test="confirm-clear"]').trigger('click')
    await flushPromises()

    expect(api.deleteStatusCheckAccounts).toHaveBeenCalledWith({ group_id: 2, account_ids: [21, 22] })
    expect(api.showWarning).toHaveBeenCalled()
    const view = wrapper.vm as unknown as {
      stats: { total: number; completed: number; unauthorized: number }
      accountsByClearableCategory: { unauthorized: Array<{ accountId: number }> }
    }
    expect(view.stats).toMatchObject({ total: 1, completed: 1, unauthorized: 1 })
    expect(view.accountsByClearableCategory.unauthorized.map((account) => account.accountId)).toEqual([22])
    expect(wrapper.get('[role="log"]').text()).toContain('database busy')
  })
})
