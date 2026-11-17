type Watcher = { remove(): void }

// Device-independent lifecycle, including permission/watch promises that cannot
// be aborted by Expo. Late watchers are removed instead of reactivating GPS.
export class LocationSharing<P extends { timestamp: number }> {
  private controller = new AbortController()
  private watcher: Watcher | null = null
  private latest: P | null = null
  private reportedAt = 0
  private posting = false
  private starting = false
  active = false

  async start(acquire: (current: () => boolean, receive: (position: P) => void) => Promise<Watcher | null>) {
    if (this.starting || this.active) return false
    this.stop()
    const controller = this.controller = new AbortController()
    const current = () => this.controller === controller && !controller.signal.aborted
    this.starting = true
    try {
      const watcher = await acquire(current, position => { if (current()) this.latest = position })
      if (!current()) { watcher?.remove(); return false }
      this.watcher = watcher
      this.active = Boolean(watcher)
      return this.active
    } finally { if (current()) this.starting = false }
  }

  stop() {
    this.controller.abort()
    this.watcher?.remove()
    this.watcher = null; this.latest = null; this.reportedAt = 0
    this.active = false; this.starting = false
  }

  async report(send: (position: P, signal: AbortSignal) => Promise<unknown>) {
    const position = this.latest
    if (!this.active || this.posting || !position || position.timestamp <= this.reportedAt) return
    this.posting = true
    const controller = this.controller
    try {
      await send(position, controller.signal)
      if (!controller.signal.aborted) this.reportedAt = position.timestamp
    } finally { this.posting = false }
  }
}