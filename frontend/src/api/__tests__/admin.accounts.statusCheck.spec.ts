import { beforeEach, describe, expect, it, vi } from 'vitest'

const { buildApiUrl } = vi.hoisted(() => ({
  buildApiUrl: vi.fn((path: string) => `/api/v1${path}`)
}))

vi.mock('@/api/client', () => ({
  apiClient: {},
  buildApiUrl
}))

import { runStatusCheck } from '@/api/admin/accounts'

describe('admin account status check API', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
  })

  it('parses fragmented SSE events and ignores heartbeat comments', async () => {
    localStorage.setItem('auth_token', 'test-token')
    const encoder = new TextEncoder()
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode(': heartbeat\n\nevent: batch_start\ndata: {"type":"batch_'))
        controller.enqueue(encoder.encode('start","total":2}\n\nevent: account_result\ndata: {"type":"account_result","account_id":7,'))
        controller.enqueue(encoder.encode('"category":"forbidden","http_status":403,"error":"Access forbidden (403) | consecutive_403=3/3"}\n\n'))
        controller.close()
      }
    })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(stream, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
    )
    const events: Array<{ type: string; error?: string }> = []

    await runStatusCheck(
      { group_id: 3, model_id: 'gpt-5.5', mode: 'default' },
      (event) => events.push(event)
    )

    expect(events).toHaveLength(2)
    expect(events[0]).toMatchObject({ type: 'batch_start', total: 2 })
    expect(events[1].error).toContain('consecutive_403=3/3')
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/admin/accounts/status-check',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ group_id: 3, model_id: 'gpt-5.5', mode: 'default' }),
        headers: expect.objectContaining({ Authorization: 'Bearer test-token' })
      })
    )
  })

  it('surfaces a JSON API error before opening the stream', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ message: 'a status check is already running for this group' }), {
        status: 409,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await expect(
      runStatusCheck({ group_id: 3, model_id: 'gpt-5.5', mode: 'default' }, vi.fn())
    ).rejects.toThrow('a status check is already running for this group')
  })
})
