import { Component, type ReactNode } from 'react'

export class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() { return { failed: true } }
  render() {
    if (this.state.failed) return <main className="fatal-screen" role="alert">
      <h1>This workspace could not be displayed</h1>
      <p>Reload to clear the in-memory session. A pending change may have completed; reconcile its status before submitting again.</p>
      <button onClick={() => location.reload()}>Reload and clear session</button>
    </main>
    return this.props.children
  }
}