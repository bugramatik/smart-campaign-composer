export interface SSEMessage {
  event: string
  data: string
}

// SSEParser turns a byte stream into server-sent events. EventSource cannot
// POST, so the generation stream is read with fetch and parsed here.
export class SSEParser {
  private buf = ''
  private event = ''
  private data: string[] = []

  push(chunk: string): SSEMessage[] {
    this.buf += chunk
    const out: SSEMessage[] = []
    let nl: number
    while ((nl = this.buf.search(/\r\n|\r|\n/)) >= 0) {
      const line = this.buf.slice(0, nl)
      const sep = this.buf.startsWith('\r\n', nl) ? 2 : 1
      this.buf = this.buf.slice(nl + sep)
      if (line === '') {
        if (this.data.length > 0) out.push({ event: this.event || 'message', data: this.data.join('\n') })
        this.event = ''
        this.data = []
        continue
      }
      if (line.startsWith(':')) continue
      const colon = line.indexOf(':')
      const field = colon < 0 ? line : line.slice(0, colon)
      let value = colon < 0 ? '' : line.slice(colon + 1)
      if (value.startsWith(' ')) value = value.slice(1)
      if (field === 'event') this.event = value
      else if (field === 'data') this.data.push(value)
    }
    return out
  }
}
