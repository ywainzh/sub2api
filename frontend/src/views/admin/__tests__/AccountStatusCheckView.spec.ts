import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  getAllIncludingInactive: vi.fn(),
  getModelsListCandidates: vi.fn(),
  list: vi.fn(),
  runStatusCheck: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      getAllIncludingInactive: api.getAllIncludingInactive,
      getModelsListCandidates: api.getModelsListCandidates
    },
    accounts: {
      list: api.list,
      runStatusCheck: api.runStatusCheck
    }
  }
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(undefined) })
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
  })

  it('defaults to gpt-5.5 and the regular test mode while loading OpenAI groups', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(api.getModelsListCandidates).toHaveBeenCalledWith(2, 'openai')
    expect(api.list).toHaveBeenCalledWith(1, 1000, { group: '2', lite: 'false' })
    expect((wrapper.vm as unknown as { selectedModelId: string }).selectedModelId).toBe('gpt-5.5')
    expect((wrapper.vm as unknown as { selectedMode: string }).selectedMode).toBe('default')
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
})
