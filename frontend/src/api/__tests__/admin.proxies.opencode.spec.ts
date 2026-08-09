import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post, postForm } = vi.hoisted(() => ({
  post: vi.fn(),
  postForm: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { post, postForm }
}))

import { importOpenCodeProxies, probeOpenCodeNodes, syncSubscription } from '@/api/admin/proxies'

describe('admin OpenCode proxy operations', () => {
  beforeEach(() => {
    post.mockReset()
    post.mockResolvedValue({ data: [] })
    postForm.mockReset()
    postForm.mockResolvedValue({ data: {} })
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

  it('uploads proxy files as browser-managed multipart form data', async () => {
    const file = new File(['http://user:password@192.0.2.10:8080'], 'proxies.txt', {
      type: 'text/plain'
    })

    await importOpenCodeProxies(file, 'external-proxies', 7)

    expect(postForm).toHaveBeenCalledOnce()
    const [url, body] = postForm.mock.calls[0]
    expect(url).toBe('/admin/opencode/proxies/import')
    expect(body).toBeInstanceOf(FormData)
    expect(body.get('file')).toBe(file)
    expect(body.get('name')).toBe('external-proxies')
    expect(body.get('expires_in_days')).toBe('7')
  })

  it('keeps uploaded sources permanent when no validity is selected', async () => {
    const file = new File(['http://192.0.2.10:8080'], 'permanent.txt', { type: 'text/plain' })

    await importOpenCodeProxies(file)

    const body = postForm.mock.calls[0][1] as FormData
    expect(body.has('expires_in_days')).toBe(false)
  })
})
