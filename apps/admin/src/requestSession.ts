import { RequestScope } from './transport.ts'

export class RequestSession {
  private current = new RequestScope()
  private busy = false
  get scope() { return this.current }
  activate() {
    // StrictMode setup/cleanup/setup must create a new abort scope.
    if (this.current.signal.aborted) this.current = new RequestScope()
    this.busy = false
  }
  begin() {
    if (this.busy || this.current.signal.aborted) return null
    this.busy = true
    return this.current
  }
  finish(scope: RequestScope) {
    // An old request's finally must not unlock a new identity's operation.
    if (scope === this.current) this.busy = false
  }
  close() { this.current.close(); this.busy = false }
}