import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import OpenCodeProxyPool from '../OpenCodeProxyPool.vue'

const api = vi.hoisted(() => ({
  listSubscriptions: vi.fn(),
  listSubscriptionNodes: vi.fn(),
  createSubscription: vi.fn(),
  updateSubscription: vi.fn(),
  deleteSubscription: vi.fn(),
  syncSubscription: vi.fn(),
  probeOpenCodeNodes: vi.fn(),
  getOpenCodeModels: vi.fn(),
  refreshOpenCodeModels: vi.fn(),
  getOpenCodePool: vi.fn(),
  updateOpenCodePool: vi.fn(),
  listOpenCodePoolWorkers: vi.fn(),
  reconcileOpenCodePool: vi.fn()
}))
const notifications = vi.hoisted(() => ({
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: { proxies: api } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notifications }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        key === 'admin.proxies.openCode.savedSyncFailed'
          ? `saved, sync failed: ${params?.error}`
          : key
    })
  }
})

const createdSubscription = {
  id: 2,
  name: 'internal',
  enabled: true,
  sync_interval_minutes: 360,
  node_count: 0,
  has_url: true,
  url_masked: 'https://example.com/***',
  created_at: '2026-08-07T00:00:00Z',
  updated_at: '2026-08-07T00:00:00Z'
}

const poolStatus = {
  id: 1,
  name: 'OpenCode Zen Pool',
  group_id: 9,
  enabled: true,
  upstream_key_configured: false,
  include_server_direct: true,
  worker_concurrency: 1,
  reconcile_status: 'ok',
  active_workers: 2,
  healthy_nodes: 2,
  rate_limited_nodes: 0,
  duplicate_nodes: 0,
  failed_nodes: 0,
  server_direct_status: 'active',
  created_at: '2026-08-07T00:00:00Z',
  updated_at: '2026-08-07T00:00:00Z'
}

describe('OpenCodeProxyPool subscription save flow', () => {
  beforeEach(() => {
    Object.values(api).forEach((mock) => mock.mockReset())
    Object.values(notifications).forEach((mock) => mock.mockReset())
    api.listSubscriptions.mockResolvedValue([])
    api.getOpenCodeModels.mockResolvedValue({ ids: [], count: 0, using_baseline: true })
    api.getOpenCodePool.mockResolvedValue(poolStatus)
    api.listOpenCodePoolWorkers.mockResolvedValue([])
    api.reconcileOpenCodePool.mockResolvedValue([])
  })

  it('closes and confirms persistence before the initial sync finishes', async () => {
    let rejectSync!: (reason: Error) => void
    const pendingSync = new Promise((_, reject) => {
      rejectSync = reject
    })
    api.createSubscription.mockResolvedValue(createdSubscription)
    api.syncSubscription.mockReturnValue(pendingSync)

    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'subscriptions' },
      global: {
        stubs: {
          Icon: true,
          BaseDialog: {
            props: ['show'],
            template: '<div v-if="show"><slot /><slot name="footer" /></div>'
          }
        }
      }
    })
    await flushPromises()

    const addButton = wrapper.findAll('button').find((button) =>
      button.text().includes('admin.proxies.openCode.addSubscription')
    )
    expect(addButton).toBeDefined()
    await addButton!.trigger('click')
    await wrapper.get('#opencode-subscription-form input[type="text"]').setValue('internal')
    await wrapper.get('input[name="opencode-subscription-secret"]').setValue('https://example.com/subscription')
    await wrapper.get('#opencode-subscription-form').trigger('submit')
    await flushPromises()

    expect(api.createSubscription).toHaveBeenCalledOnce()
    expect(api.syncSubscription).toHaveBeenCalledWith(2)
    expect(wrapper.find('#opencode-subscription-form').exists()).toBe(false)
    expect(notifications.showSuccess).toHaveBeenCalledWith('common.saved')

    rejectSync(new Error('listener port conflict'))
    await flushPromises()
    expect(notifications.showWarning).toHaveBeenCalledWith(
      'saved, sync failed: listener port conflict',
      6000
    )
  })

  it('keeps pool and subscription secrets out of browser password autofill', async () => {
    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'subscriptions' },
      global: {
        stubs: {
          Icon: true,
          BaseDialog: {
            props: ['show'],
            template: '<div v-if="show"><slot /><slot name="footer" /></div>'
          }
        }
      }
    })
    await flushPromises()

    const upstreamKey = wrapper.get('#opencode-upstream-key')
    expect(upstreamKey.attributes('autocomplete')).toBe('new-password')
    expect(upstreamKey.attributes('data-1p-ignore')).toBe('')
    expect(upstreamKey.attributes('data-lpignore')).toBe('true')

    const addButton = wrapper.findAll('button').find((button) =>
      button.text().includes('admin.proxies.openCode.addSubscription')
    )
    await addButton!.trigger('click')
    const subscriptionURL = wrapper.get('input[name="opencode-subscription-secret"]')
    expect(subscriptionURL.attributes('autocomplete')).toBe('new-password')
    expect(subscriptionURL.attributes('data-bwignore')).toBe('true')
  })
})
