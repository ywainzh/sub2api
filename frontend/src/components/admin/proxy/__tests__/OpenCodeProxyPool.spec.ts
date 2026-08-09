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
	createOpenCodeProbeJob: vi.fn(),
	getOpenCodeMaintenanceJob: vi.fn(),
	getOpenCodeMaintenance: vi.fn(),
	importOpenCodeProxies: vi.fn(),
	deleteOpenCodeManagedNode: vi.fn(),
  getOpenCodeModels: vi.fn(),
  refreshOpenCodeModels: vi.fn(),
  getOpenCodePool: vi.fn(),
  listOpenCodePoolWorkers: vi.fn()
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
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'admin.proxies.openCode.savedSyncFailed') {
          return `saved, sync failed: ${params?.error}`
        }
        if (key === 'admin.proxies.openCode.probeInProgress') {
          return `probing ${params?.count} nodes`
        }
        if (key === 'admin.proxies.openCode.probeCompleted') {
          return `complete: ${params?.healthy}/${params?.rateLimited}/${params?.duplicate}/${params?.failed}`
        }
		if (key === 'admin.proxies.openCode.jobProgress') return `${params?.processed}/${params?.total}`
		if (key === 'admin.proxies.openCode.jobCompleted') return `job: ${params?.healthy}/${params?.failed}`
        return key
      }
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
	updated_at: '2026-08-07T00:00:00Z',
	source_type: 'url'
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
  probe_interval_minutes: 15,
  server_direct_status: 'active',
  created_at: '2026-08-07T00:00:00Z',
  updated_at: '2026-08-07T00:00:00Z'
}

const managedNode = {
  id: 28,
  subscription_id: 2,
  proxy_id: 40,
  node_key: 'node-28',
  display_name: 'JP node',
  mihomo_name: 'managed-28',
  protocol: 'vmess',
  listener_port: 22021,
	transport_mode: 'mihomo_listener',
  sync_status: 'active',
  health_status: 'healthy',
  exit_ip: '203.0.113.28',
  latency_ms: 250,
  opencode_http_status: 200,
	last_probe_at: '2026-08-07T00:00:00Z',
	consecutive_failures: 0,
	retry_count: 0
}

const rateLimitedNode = {
  ...managedNode,
  id: 29,
  proxy_id: 41,
  node_key: 'node-29',
  display_name: 'US rate limited node',
  mihomo_name: 'managed-29',
  listener_port: 22022,
  health_status: 'rate_limited',
  exit_ip: '203.0.113.29',
  opencode_http_status: 429,
  failure_type: 'rate_limited'
}

const authFailedNode = {
  ...managedNode,
  id: 30,
  proxy_id: 42,
  node_key: 'node-30',
  display_name: 'US auth failed node',
  mihomo_name: 'managed-30',
  listener_port: 22023,
  health_status: 'auth_error',
  exit_ip: '203.0.113.30',
  opencode_http_status: 403,
  failure_type: 'auth'
}

describe('OpenCodeProxyPool subscription save flow', () => {
  beforeEach(() => {
    Object.values(api).forEach((mock) => mock.mockReset())
    Object.values(notifications).forEach((mock) => mock.mockReset())
    api.listSubscriptions.mockResolvedValue([])
    api.getOpenCodeModels.mockResolvedValue({ ids: [], count: 0, using_baseline: true })
    api.getOpenCodePool.mockResolvedValue(poolStatus)
    api.listOpenCodePoolWorkers.mockResolvedValue([])
	api.getOpenCodeMaintenance.mockResolvedValue({ next_run_at: '2026-08-08T16:00:00Z' })
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

  it.each(['subscriptions', 'nodes'] as const)('hides default pool configuration in the %s view', async (view) => {
    const wrapper = mount(OpenCodeProxyPool, {
      props: { view },
      global: {
        stubs: {
          Icon: true,
          BaseDialog: true
        }
      }
    })
    await flushPromises()

    expect(wrapper.find('#opencode-worker-concurrency').exists()).toBe(false)
    expect(wrapper.find('#opencode-upstream-key').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('admin.proxies.openCode.enablePool')
    expect(wrapper.text()).not.toContain('admin.proxies.openCode.reconcile')
  })

  it('keeps the subscription secret out of browser password autofill', async () => {
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
    await addButton!.trigger('click')
    const subscriptionURL = wrapper.get('input[name="opencode-subscription-secret"]')
    expect(subscriptionURL.attributes('autocomplete')).toBe('new-password')
    expect(subscriptionURL.attributes('data-bwignore')).toBe('true')
  })

  it('shows honest progress immediately and summarizes the completed batch', async () => {
	vi.useFakeTimers()
    api.listSubscriptions.mockResolvedValue([createdSubscription])
    api.listSubscriptionNodes.mockResolvedValue([managedNode])
	api.createOpenCodeProbeJob.mockResolvedValue({ id: 7, status: 'pending', processed_nodes: 0, total_nodes: 1 })
	api.getOpenCodeMaintenanceJob
	  .mockResolvedValueOnce({ id: 7, status: 'running', processed_nodes: 0, total_nodes: 1 })
	  .mockResolvedValueOnce({
		id: 7, status: 'completed', processed_nodes: 1, total_nodes: 1,
		healthy_nodes: 1, failed_nodes: 0, rate_limited_nodes: 0, duplicate_nodes: 0
	  })

    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'nodes' },
      global: {
        stubs: {
          Icon: true,
          BaseDialog: true
        }
      }
    })
    await flushPromises()

    const probeButton = wrapper.get('[data-testid="opencode-probe-all"]')
    await probeButton.trigger('click')

	expect(api.createOpenCodeProbeJob).toHaveBeenCalledOnce()
	expect(api.createOpenCodeProbeJob).toHaveBeenCalledWith([28])
    expect(probeButton.attributes('disabled')).toBeDefined()
    expect(probeButton.attributes('aria-busy')).toBe('true')
    expect(probeButton.text()).toContain('admin.proxies.openCode.probing')
	expect(wrapper.get('[data-testid="opencode-probe-progress"]').text()).toContain('0/1')
    expect(wrapper.get('[data-testid="opencode-node-probing"]').exists()).toBe(true)

	await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(wrapper.find('[data-testid="opencode-probe-progress"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="opencode-node-probing"]').exists()).toBe(false)
	expect(notifications.showSuccess).toHaveBeenCalledWith('job: 1/0', 6000)
	wrapper.unmount()
	vi.useRealTimers()
  })

  it('probes only the selected node from its row action', async () => {
    let resolveProbe!: (results: Array<Record<string, unknown>>) => void
    const pendingProbe = new Promise<Array<Record<string, unknown>>>((resolve) => {
      resolveProbe = resolve
    })
    api.listSubscriptions.mockResolvedValue([createdSubscription])
    api.listSubscriptionNodes.mockResolvedValue([managedNode, rateLimitedNode])
    api.probeOpenCodeNodes.mockReturnValue(pendingProbe)

    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'nodes' },
      global: {
        stubs: {
          Icon: true,
          BaseDialog: true
        }
      }
    })
    await flushPromises()

    const rowProbeButton = wrapper.get('[data-testid="opencode-probe-node-29"]')
    await rowProbeButton.trigger('click')

    expect(api.probeOpenCodeNodes).toHaveBeenCalledOnce()
    expect(api.probeOpenCodeNodes).toHaveBeenCalledWith([29])
    expect(rowProbeButton.attributes('disabled')).toBeDefined()
    expect(rowProbeButton.attributes('aria-busy')).toBe('true')
    expect(wrapper.get('[data-testid="opencode-probe-progress"]').text()).toContain('probing 1 nodes')

    resolveProbe([
      {
        node_id: 29,
        success: true,
        health_status: 'healthy',
        exit_ip: '203.0.113.29',
        opencode_http_status: 200
      }
    ])
    await flushPromises()

    expect(wrapper.find('[data-testid="opencode-probe-progress"]').exists()).toBe(false)
    expect(notifications.showSuccess).toHaveBeenCalledWith('complete: 1/0/0/0', 6000)
    wrapper.unmount()
  })

  it('deletes a managed node only after confirmation', async () => {
    api.listSubscriptions.mockResolvedValue([createdSubscription])
    api.listSubscriptionNodes.mockResolvedValue([managedNode])
    api.deleteOpenCodeManagedNode.mockResolvedValue({ message: 'deleted' })
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'nodes' },
      global: { stubs: { Icon: true, BaseDialog: true } }
    })
    await flushPromises()

    await wrapper.get('[data-testid="opencode-delete-node-28"]').trigger('click')
    await flushPromises()

    expect(window.confirm).toHaveBeenCalledOnce()
    expect(api.deleteOpenCodeManagedNode).toHaveBeenCalledWith(28)
    expect(notifications.showSuccess).toHaveBeenCalled()
  })

  it('switches from subscriptions to the matching problem nodes when a metric is clicked', async () => {
    api.getOpenCodePool.mockResolvedValue({ ...poolStatus, rate_limited_nodes: 1, healthy_nodes: 1 })
    api.listSubscriptions.mockResolvedValue([createdSubscription])
    api.listSubscriptionNodes.mockResolvedValue([managedNode, rateLimitedNode])

    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'subscriptions' },
      global: {
        stubs: {
          Icon: true,
          BaseDialog: true
        }
      }
    })
    await flushPromises()

    await wrapper.get('[data-testid="opencode-filter-rate-limited"]').trigger('click')
    expect(wrapper.emitted('showNodes')).toEqual([[]])

    await wrapper.setProps({ view: 'nodes' })
    await flushPromises()

    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('rate_limited')
    expect(wrapper.text()).toContain('US rate limited node')
    expect(wrapper.text()).not.toContain('JP node')
  })

  it('drills the aggregate failure metric into every non-429 and non-duplicate failure', async () => {
    api.getOpenCodePool.mockResolvedValue({ ...poolStatus, failed_nodes: 1, healthy_nodes: 1 })
    api.listSubscriptions.mockResolvedValue([createdSubscription])
    api.listSubscriptionNodes.mockResolvedValue([managedNode, rateLimitedNode, authFailedNode])

    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'nodes' },
      global: {
        stubs: {
          Icon: true,
          BaseDialog: true
        }
      }
    })
    await flushPromises()

    await wrapper.get('[data-testid="opencode-filter-failed"]').trigger('click')

    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('failed')
    expect(wrapper.text()).toContain('US auth failed node')
    expect(wrapper.text()).not.toContain('US rate limited node')
    expect(wrapper.text()).not.toContain('JP node')
  })
})
