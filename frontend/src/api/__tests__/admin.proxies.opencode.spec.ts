import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { post }
}))

import { probeOpenCodeNodes, syncSubscription } from '@/api/admin/proxies'

describe('admin OpenCode proxy operations', () => {
  beforeEach(() => {
    post.mockReset()
    post.mockResolvedValue({ data: [] })
  })

  it('allows subscription synchronization to outlive the global client timeout', async () => {
    await syncSubscription(3)

    expect(post).toHaveBeenCalledWith(
      '/admin/proxy-subscriptions/3/sync',
      undefined,
      { timeout: 10 * 60 * 1000 }
    )
  })

  it('allows a large batch probe to finish', async () => {
    await probeOpenCodeNodes([41, 42])

    expect(post).toHaveBeenCalledWith(
      '/admin/opencode/proxies/probe',
      { node_ids: [41, 42] },
      { timeout: 10 * 60 * 1000 }
    )
  })
})
