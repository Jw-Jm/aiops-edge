import { describe, expect, it, vi } from 'vitest'
import { streamRunEvents, type RunEvent } from './client'

function sseResponse(frame: string) {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(frame))
      controller.close()
    },
  })
  return { ok: true, status: 200, body }
}

describe('Run SSE resume', () => {
  it('retains id and resumes with Last-Event-ID after a disconnect', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(sseResponse('id: 7\ndata: {"sequence":7,"event_type":"started"}\n\n'))
      .mockResolvedValueOnce(sseResponse('id: 8\ndata: {"sequence":8,"event_type":"finished"}\n\n'))
    vi.stubGlobal('fetch', fetchMock)
    const events: RunEvent[] = []

    await streamRunEvents('run-1', (event) => events.push(event), undefined, { maxReconnects: 1 })

    expect(events.map((event) => event.sequence)).toEqual([7, 8])
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/ai/runs/run-1/events?after_sequence=7', expect.objectContaining({
      headers: expect.objectContaining({ 'Last-Event-ID': '7' }),
    }))
    vi.unstubAllGlobals()
  })
})
