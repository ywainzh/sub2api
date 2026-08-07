import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import UserApiKeysModal from '../UserApiKeysModal.vue'

const usersAPI = vi.hoisted(() => ({ getUserApiKeys: vi.fn() }))
const groupsAPI = vi.hoisted(() => ({ getAll: vi.fn() }))
const proxiesAPI = vi.hoisted(() => ({ getOpenCodePool: vi.fn() }))
const apiKeysAPI = vi.hoisted(() => ({
  updateApiKeyGroup: vi.fn(),
  bindOpenCodePool: vi.fn(),
  unbindOpenCodePool: vi.fn()
}))
const notifications = vi.hoisted(() => ({ showSuccess: vi.fn(), showError: vi.fn() }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: usersAPI,
    groups: groupsAPI,
    proxies: proxiesAPI,
    apiKeys: apiKeysAPI
  }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => notifications }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const user = {
  id: 7,
  email: 'admin@example.com',
  username: 'admin'
}

const apiKey = {
  id: 3,
  user_id: 7,
  key: 'sk-test-key-3-abcdefghijklmnopqrstuvwxyz',
  name: '测试国产模型',
  group_id: 5,
  opencode_bound: false,
  status: 'active',
  created_at: '2026-08-07T00:00:00Z'
}

const pool = {
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
  created_at: '2026-08-07T00:00:00Z',
  updated_at: '2026-08-07T00:00:00Z'
}

describe('UserApiKeysModal OpenCode binding', () => {
  beforeEach(() => {
    Object.values(usersAPI).forEach((mock) => mock.mockReset())
    Object.values(groupsAPI).forEach((mock) => mock.mockReset())
    Object.values(proxiesAPI).forEach((mock) => mock.mockReset())
    Object.values(apiKeysAPI).forEach((mock) => mock.mockReset())
    Object.values(notifications).forEach((mock) => mock.mockReset())
    usersAPI.getUserApiKeys.mockResolvedValue({ items: [{ ...apiKey }] })
    groupsAPI.getAll.mockResolvedValue([])
    proxiesAPI.getOpenCodePool.mockResolvedValue(pool)
    apiKeysAPI.bindOpenCodePool.mockResolvedValue({ opencode_bound: true })
    apiKeysAPI.unbindOpenCodePool.mockResolvedValue({ opencode_bound: false })
  })

  it('binds and unbinds a key through the accessible pool switch', async () => {
    const wrapper = mount(UserApiKeysModal, {
      props: { show: true, user: user as never },
      global: {
        stubs: {
          BaseDialog: {
            props: ['show'],
            template: '<div v-if="show"><slot /></div>'
          },
          GroupBadge: true,
          GroupOptionItem: true,
          Teleport: true
        }
      }
    })
    await flushPromises()

    const toggle = wrapper.get('[role="switch"]')
    expect(toggle.attributes('aria-checked')).toBe('false')
    await toggle.trigger('click')
    await flushPromises()

    expect(apiKeysAPI.bindOpenCodePool).toHaveBeenCalledWith(3)
    expect(toggle.attributes('aria-checked')).toBe('true')
    expect(notifications.showSuccess).toHaveBeenCalledWith('admin.users.openCodeBindingUpdated')

    await toggle.trigger('click')
    await flushPromises()
    expect(apiKeysAPI.unbindOpenCodePool).toHaveBeenCalledWith(3)
    expect(toggle.attributes('aria-checked')).toBe('false')
  })

  it('does not allow a new binding while the pool has no active worker', async () => {
    proxiesAPI.getOpenCodePool.mockResolvedValue({ ...pool, active_workers: 0 })
    const wrapper = mount(UserApiKeysModal, {
      props: { show: true, user: user as never },
      global: {
        stubs: {
          BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
          GroupBadge: true,
          GroupOptionItem: true,
          Teleport: true
        }
      }
    })
    await flushPromises()

    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('admin.users.openCodePoolUnavailable')
  })
})
