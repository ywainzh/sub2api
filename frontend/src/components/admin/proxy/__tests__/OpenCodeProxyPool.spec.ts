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
const PaginationStub = {
  name: 'Pagination',
  props: ['page', 'pageSize', 'total'],
  emits: ['update:page', 'update:pageSize'],
  template: `
    <div>
      <span data-testid="pagination-state">{{ page }}/{{ pageSize }}/{{ total }}</span>
      <button data-testid="pagination-next" @click="$emit('update:page', page + 1)">next</button>
      <button data-testid="pagination-size" @click="$emit('update:pageSize', 10)">size</button>
    </div>
  `
}

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
    vi.useRealTimers()
    window.localStorage.setItem('table-page-size', '20')
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

  it('imports a short-lived proxy source with a selected seven-day validity', async () => {
    api.importOpenCodeProxies.mockResolvedValue({ id: 18, status: 'pending', processed_nodes: 0, total_nodes: 1 })
    api.getOpenCodeMaintenanceJob.mockResolvedValue({
      id: 18,
      status: 'completed',
      processed_nodes: 1,
      total_nodes: 1,
      healthy_nodes: 1,
      failed_nodes: 0,
      rate_limited_nodes: 0,
      duplicate_nodes: 0
    })
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

    const importButton = wrapper.findAll('button').find((button) =>
      button.text().includes('admin.proxies.openCode.importNodes')
    )
    await importButton!.trigger('click')
    const file = new File(['http://user:password@192.0.2.10:3129'], 'proxyscrape-trial.txt', {
      type: 'text/plain'
    })
    const fileInput = wrapper.get('#opencode-import-file')
    Object.defineProperty(fileInput.element, 'files', { configurable: true, value: [file] })
    await fileInput.trigger('change')
    await wrapper.get('#opencode-import-validity').setValue('7')
    await wrapper.get('#opencode-import-form').trigger('submit')
    await flushPromises()

    expect(api.importOpenCodeProxies).toHaveBeenCalledWith(file, 'proxyscrape-trial', 7)
    expect(api.getOpenCodeMaintenanceJob).toHaveBeenCalledWith(18)
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
    expect(rowProbeButton.classes()).toContain('flex-col')
    expect(rowProbeButton.classes()).toContain('hover:bg-emerald-50')
    expect(rowProbeButton.classes()).not.toContain('btn-secondary')
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

    const deleteButton = wrapper.get('[data-testid="opencode-delete-node-28"]')
    expect(deleteButton.classes()).toContain('flex-col')
    expect(deleteButton.classes()).toContain('hover:bg-red-50')
    expect(deleteButton.classes()).not.toContain('btn-danger')
    await deleteButton.trigger('click')
    await flushPromises()

    expect(window.confirm).toHaveBeenCalledOnce()
    expect(api.deleteOpenCodeManagedNode).toHaveBeenCalledWith(28)
    expect(notifications.showSuccess).toHaveBeenCalled()
  })

  it('flags a baseline model registry and reveals the ids and error once expanded', async () => {
    api.getOpenCodeModels.mockResolvedValue({
      ids: ['big-pickle', 'x-preview-f-free'],
      count: 2,
      last_fetched_at: '2026-08-24T02:00:00Z',
      last_error: 'upstream 503',
      using_baseline: true
    })
    api.refreshOpenCodeModels.mockResolvedValue({ ids: ['big-pickle'], count: 1, using_baseline: false })

    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'subscriptions' },
      global: { stubs: { Icon: true, BaseDialog: true } }
    })
    await flushPromises()

    const panel = wrapper.get('[data-testid="opencode-model-registry"]')
    expect(panel.classes()).toContain('border-amber-300')
    expect(panel.text()).toContain('admin.proxies.openCode.modelRegistryBaseline')
    expect(panel.text()).not.toContain('big-pickle')

    await wrapper.get('[data-testid="opencode-model-registry-toggle"]').trigger('click')
    expect(panel.text()).toContain('big-pickle')
    expect(panel.text()).toContain('x-preview-f-free')
    expect(panel.get('[role="alert"]').text()).toBe('upstream 503')

    await wrapper.get('[data-testid="opencode-model-registry-refresh"]').trigger('click')
    await flushPromises()
    expect(api.refreshOpenCodeModels).toHaveBeenCalledOnce()
    expect(panel.classes()).not.toContain('border-amber-300')
    expect(panel.text()).toContain('admin.proxies.openCode.modelRegistryLive')
  })

  it('attributes each node to its subscription and stacks both filters', async () => {
    const otherSubscription = { ...createdSubscription, id: 3, name: 'external' }
    api.listSubscriptions.mockResolvedValue([createdSubscription, otherSubscription])
    api.listSubscriptionNodes.mockImplementation((id: number) =>
      Promise.resolve(
        id === 2
          ? [managedNode, authFailedNode]
          : [{ ...managedNode, id: 40, subscription_id: 3, node_key: 'node-40', display_name: 'EU node' }]
      )
    )

    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'nodes' },
      global: { stubs: { Icon: true, BaseDialog: true } }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('internal')
    expect(wrapper.text()).toContain('external')

    await wrapper.get('[data-testid="opencode-subscription-filter"]').setValue('2')
    expect(wrapper.text()).toContain('JP node')
    expect(wrapper.text()).toContain('US auth failed node')
    expect(wrapper.text()).not.toContain('EU node')

    await wrapper.get('select').setValue('failed')
    expect(wrapper.text()).toContain('US auth failed node')
    expect(wrapper.text()).not.toContain('JP node')
    expect(wrapper.text()).not.toContain('EU node')
  })

  it('breaks the subscription node count down by health', async () => {
    api.listSubscriptions.mockResolvedValue([
      {
        ...createdSubscription,
        node_count: 5,
        healthy_node_count: 3,
        failed_node_count: 2,
        inactive_node_count: 1
      }
    ])

    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'subscriptions' },
      global: { stubs: { Icon: true, BaseDialog: true } }
    })
    await flushPromises()

    const breakdown = wrapper.get('[data-testid="opencode-subscription-nodes-2"]')
    expect(breakdown.findAll('span').map((span) => span.text())).toEqual(['3', '2', '1'])
    expect(breakdown.findAll('span')[1].classes()).toContain('badge-danger')
  })

  it('drops the deleted node immediately and reports the background follow-up', async () => {
    vi.useFakeTimers()
    api.listSubscriptions.mockResolvedValue([createdSubscription])
    api.listSubscriptionNodes.mockResolvedValue([managedNode, authFailedNode])
    api.deleteOpenCodeManagedNode.mockResolvedValue({ message: 'queued' })
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'nodes' },
      global: { stubs: { Icon: true, BaseDialog: true } }
    })
    await flushPromises()

    await wrapper.get('[data-testid="opencode-delete-node-28"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).not.toContain('JP node')
    expect(wrapper.text()).toContain('US auth failed node')
    // The row is dropped locally, so no second full reload is issued.
    expect(api.listSubscriptionNodes).toHaveBeenCalledTimes(1)

    api.getOpenCodePool.mockResolvedValue({
      ...poolStatus,
      last_reconciled_at: '2026-08-24T03:00:00Z'
    })
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(notifications.showSuccess).toHaveBeenCalledWith('admin.proxies.openCode.nodeDeleteFollowUpDone')
    expect(api.listSubscriptionNodes).toHaveBeenCalledTimes(1)
    vi.useRealTimers()
  })

  it('surfaces a failed background follow-up after deletion', async () => {
    vi.useFakeTimers()
    api.listSubscriptions.mockResolvedValue([createdSubscription])
    api.listSubscriptionNodes.mockResolvedValue([managedNode])
    api.deleteOpenCodeManagedNode.mockResolvedValue({ message: 'queued' })
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'nodes' },
      global: { stubs: { Icon: true, BaseDialog: true } }
    })
    await flushPromises()

    await wrapper.get('[data-testid="opencode-delete-node-28"]').trigger('click')
    await flushPromises()

    api.getOpenCodePool.mockResolvedValue({
      ...poolStatus,
      reconcile_status: 'error',
      reconcile_error: 'mihomo unreachable'
    })
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(notifications.showError).toHaveBeenCalledWith('admin.proxies.openCode.nodeDeleteFollowUpFailed')
    expect(wrapper.text()).toContain('mihomo unreachable')
    vi.useRealTimers()
  })

  it('renders the aggregate failure metric as non-interactive text', async () => {
    api.getOpenCodePool.mockResolvedValue({
      ...poolStatus,
      rate_limited_nodes: 1,
      duplicate_nodes: 2,
      failed_nodes: 3
    })
    api.listSubscriptions.mockResolvedValue([createdSubscription])

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

    const summary = wrapper.get('[data-testid="opencode-failure-summary"]')
    expect(summary.text()).toBe('1 / 2 / 3')
    expect(summary.findAll('button')).toHaveLength(0)
    expect(summary.attributes('title')).toBeUndefined()
  })

  it('filters aggregate failures through the existing health dropdown', async () => {
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

    await wrapper.get('select').setValue('failed')

    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('failed')
    expect(wrapper.text()).toContain('US auth failed node')
    expect(wrapper.text()).not.toContain('US rate limited node')
    expect(wrapper.text()).not.toContain('JP node')
  })

  it('paginates the subscription list independently', async () => {
    api.listSubscriptions.mockResolvedValue(
      Array.from({ length: 21 }, (_, index) => ({
        ...createdSubscription,
        id: index + 1,
        name: `subscription-${String(index + 1).padStart(2, '0')}`
      }))
    )

    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'subscriptions' },
      global: { stubs: { Icon: true, BaseDialog: true, Pagination: PaginationStub } }
    })
    await flushPromises()

    expect(wrapper.get('[data-testid="opencode-subscription-pagination"]').text()).toContain('1/20/21')
    expect(wrapper.findAll('tbody tr')).toHaveLength(20)
    expect(wrapper.text()).toContain('subscription-01')
    expect(wrapper.text()).not.toContain('subscription-21')

    await wrapper.get('[data-testid="pagination-next"]').trigger('click')

    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('subscription-01')
    expect(wrapper.text()).toContain('subscription-21')
  })

  it('paginates filtered nodes and resets to the first page when page size changes', async () => {
    api.listSubscriptions.mockResolvedValue([createdSubscription])
    api.listSubscriptionNodes.mockResolvedValue(
      Array.from({ length: 21 }, (_, index) => ({
        ...managedNode,
        id: index + 1,
        node_key: `node-${index + 1}`,
        display_name: `node-${String(index + 1).padStart(2, '0')}`
      }))
    )

    const wrapper = mount(OpenCodeProxyPool, {
      props: { view: 'nodes' },
      global: { stubs: { Icon: true, BaseDialog: true, Pagination: PaginationStub } }
    })
    await flushPromises()

    expect(wrapper.get('[data-testid="opencode-node-pagination"]').text()).toContain('1/20/21')
    expect(wrapper.findAll('tbody tr')).toHaveLength(20)

    await wrapper.get('[data-testid="pagination-next"]').trigger('click')
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.text()).toContain('node-21')

    await wrapper.get('[data-testid="pagination-size"]').trigger('click')
    expect(wrapper.get('[data-testid="opencode-node-pagination"]').text()).toContain('1/10/21')
    expect(wrapper.findAll('tbody tr')).toHaveLength(10)
    expect(wrapper.text()).toContain('node-01')
    expect(wrapper.text()).not.toContain('node-21')
  })
})
