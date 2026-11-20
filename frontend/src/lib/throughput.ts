// Throughput tracks how many messages per second moved to "sent", derived
// from successive dashboard snapshots (the API reports totals, not rates).
export class Throughput {
  private last = new Map<string, { sent: number; at: number }>()
  readonly series = new Map<string, number[]>()

  constructor(private readonly window = 60) {}

  push(channel: string, sent: number, at: number): number {
    const prev = this.last.get(channel)
    this.last.set(channel, { sent, at })
    if (!prev || at <= prev.at) return 0
    const rate = Math.max(0, (sent - prev.sent) / ((at - prev.at) / 1000))
    const s = this.series.get(channel) ?? []
    s.push(rate)
    if (s.length > this.window) s.shift()
    this.series.set(channel, s)
    return rate
  }
}
