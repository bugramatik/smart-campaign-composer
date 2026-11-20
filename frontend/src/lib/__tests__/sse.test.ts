import { describe, expect, it } from 'vitest'
import { SSEParser } from '../sse'

describe('SSEParser', () => {
  it('parses events split across arbitrary chunks', () => {
    const stream = 'event: status\ndata: {"stage":"segment"}\n\nevent: done\ndata: {"ok":true}\n\n'
    for (let size = 1; size <= stream.length; size++) {
      const p = new SSEParser()
      const out = []
      for (let i = 0; i < stream.length; i += size) out.push(...p.push(stream.slice(i, i + size)))
      expect(out).toEqual([
        { event: 'status', data: '{"stage":"segment"}' },
        { event: 'done', data: '{"ok":true}' },
      ])
    }
  })

  it('joins multi-line data, ignores comments and handles CRLF', () => {
    const p = new SSEParser()
    expect(p.push(': keep-alive\r\ndata: a\r\ndata: b\r\n\r\n')).toEqual([{ event: 'message', data: 'a\nb' }])
  })

  it('drops events without data', () => {
    expect(new SSEParser().push('event: ping\n\n')).toEqual([])
  })
})
