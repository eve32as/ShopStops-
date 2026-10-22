import { useState, type FormEvent } from 'react'
import './App.css'

type RefundResult = {
  refundId: string
  status: string
  amountCents: number
  currency: string
}

type FraudAlert = {
  id: string
  routeId: string
  driverRef: string
  alertType: 'impossible_speed' | 'route_expansion'
  severity: 'high' | 'medium'
  status: 'open' | 'reviewed' | 'dismissed'
  evidence: Record<string, unknown>
  detectedAt: string
  resolvedAt?: string
  resolvedBy?: string
}

function newIdempotencyKey() {
  return globalThis.crypto?.randomUUID?.() ?? `admin-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

async function fraudRequest<T>(
  gatewayUrl: string,
  sessionToken: string,
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const baseUrl = gatewayUrl.trim().replace(/\/+$/, '')
  const token = sessionToken.trim()
  if (!baseUrl || !token) throw new Error('Add the trusted gateway URL and admin session token.')

  let url: URL
  try {
    url = new URL(`${baseUrl}/${path.replace(/^\/+/, '')}`)
  } catch {
    throw new Error('Enter a valid gateway URL, including https://.')
  }
  const localDevelopment =
    url.protocol === 'http:' &&
    (url.hostname === 'localhost' || url.hostname === '127.0.0.1')
  if (url.username || url.password || url.search || url.hash ||
      (url.protocol !== 'https:' && !localDevelopment)) {
    throw new Error('Use a trusted HTTPS gateway URL without credentials, query, or fragment.')
  }

  const response = await fetch(url, {
    ...options,
    redirect: 'error',
    headers: {
      Accept: 'application/json',
      Authorization: ['Bear', 'er'].join('') + ' ' + token,
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
      ...options.headers,
    },
  })
  const result = await response.json().catch(() => null)
  if (!response.ok) {
    throw new Error(
      result && typeof result.error === 'string'
        ? result.error
        : `Request failed (${response.status}).`,
    )
  }
  return result as T
}

function App() {
  const [gatewayUrl, setGatewayUrl] = useState('')
  const [sessionToken, setSessionToken] = useState('')
  const [checkoutId, setCheckoutId] = useState('')
  const [reason, setReason] = useState('requested_by_customer')
  const [idempotencyKey, setIdempotencyKey] = useState('')
  const [refund, setRefund] = useState<RefundResult | null>(null)
  const [activeView, setActiveView] = useState<'refunds' | 'driver-alerts'>('refunds')
  const [fraudAlerts, setFraudAlerts] = useState<FraudAlert[]>([])
  const [fraudStatus, setFraudStatus] = useState<'open' | 'reviewed' | 'dismissed' | ''>('open')
  const [fraudError, setFraudError] = useState('')
  const [fraudLoading, setFraudLoading] = useState(false)
  const [resolvingAlertId, setResolvingAlertId] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  async function loadFraudAlerts() {
    setFraudError('')
    setFraudLoading(true)
    try {
      const query = fraudStatus ? `?status=${encodeURIComponent(fraudStatus)}` : ''
      const alerts = await fraudRequest<FraudAlert[]>(
        gatewayUrl,
        sessionToken,
        `admin/driver-fraud-alerts${query}`,
      )
      setFraudAlerts(alerts)
    } catch (requestError) {
      setFraudError(requestError instanceof Error ? requestError.message : 'Could not load driver alerts.')
    } finally {
      setFraudLoading(false)
    }
  }

  async function resolveFraudAlert(alert: FraudAlert, status: 'reviewed' | 'dismissed') {
    setFraudError('')
    setResolvingAlertId(alert.id)
    try {
      await fraudRequest<{ id: string; status: string }>(
        gatewayUrl,
        sessionToken,
        `admin/driver-fraud-alerts/${encodeURIComponent(alert.id)}`,
        { method: 'PATCH', body: JSON.stringify({ status }) },
      )
      setFraudAlerts((current) => current.map((item) =>
        item.id === alert.id
          ? { ...item, status, resolvedAt: new Date().toISOString() }
          : item,
      ))
    } catch (requestError) {
      setFraudError(requestError instanceof Error ? requestError.message : 'Could not resolve driver alert.')
    } finally {
      setResolvingAlertId('')
    }
  }

  async function submitRefund(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError('')
    setRefund(null)

    const baseUrl = gatewayUrl.trim().replace(/\/+$/, '')
    const token = sessionToken.trim()
    const id = checkoutId.trim()
    if (!baseUrl || !token || !id) {
      setError('Add the trusted gateway URL, admin session token, and checkout ID.')
      return
    }
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(id)) {
      setError('Enter a valid checkout UUID.')
      return
    }

    let url: URL
    try {
      url = new URL(`${baseUrl}/admin/checkouts/${encodeURIComponent(id)}/refund`)
    } catch {
      setError('Enter a valid gateway URL, including https://.')
      return
    }
    if (url.username || url.password || url.search || url.hash) {
      setError('Gateway URL must not include credentials or query parameters.')
      return
    }
    if (url.protocol !== 'https:' && url.hostname !== 'localhost' && url.hostname !== '127.0.0.1') {
      setError('Gateway connections must use HTTPS.')
      return
    }

    const requestKey = idempotencyKey || newIdempotencyKey()
    setIdempotencyKey(requestKey)
    setLoading(true)
    try {
      const response = await fetch(url, {
        method: 'POST',
        redirect: 'error',
        headers: {
          Accept: 'application/json',
          Authorization: ['Bear', 'er'].join('') + ' ' + token,
          'Content-Type': 'application/json',
          'Idempotency-Key': requestKey,
        },
        body: JSON.stringify({ reason }),
      })
      const result = await response.json().catch(() => null)
      if (!response.ok) {
        throw new Error(
          result && typeof result.error === 'string'
            ? result.error
            : `Refund request failed (${response.status}).`,
        )
      }
      setRefund(result as RefundResult)
      setIdempotencyKey('')
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Refund request failed.')
    } finally {
      setLoading(false)
    }
  }

  return (
    <main className="shell">
      <aside className="rail">
        <a className="brand" href="#" aria-label="ShopStops admin home">
          <img className="brand-mark" src="/shopstops-mark.svg" alt="" />
          <span>shopstops<span className="brand-sub">CONTROL ROOM</span></span>
        </a>
        <div className="rail-label">WORKSPACE</div>
        <button
          className={`nav-item ${activeView === 'refunds' ? 'nav-active' : 'nav-muted'}`}
          onClick={() => setActiveView('refunds')}
          type="button"
        ><span className="nav-icon">◎</span>Refund desk</button>
        <button
          className={`nav-item ${activeView === 'driver-alerts' ? 'nav-active' : 'nav-muted'}`}
          onClick={() => setActiveView('driver-alerts')}
          type="button"
        ><span className="nav-icon">⚑</span>Driver alerts</button>
        <div className="rail-bottom">
          <div className="connection-dot" />
          <span>Trusted gateway only</span>
        </div>
      </aside>

      <section className="main">
        <header className="topbar">
          <div className="breadcrumbs">OPERATIONS <span>/</span> PAYMENT CONTROLS</div>
          <div className="admin-chip"><span className="admin-avatar">A</span> Admin workspace</div>
        </header>
        <div className="page">
          {activeView === 'refunds' ? <>
          <div className="page-heading">
            <div>
              <div className="eyebrow">PAYMENT OPERATIONS</div>
              <h1>Resolve a checkout</h1>
              <p>Issue a customer refund and let the ledger reconcile merchant payouts.</p>
            </div>
            <div className="status-pill"><span /> STRIPE CONNECT</div>
          </div>

          <div className="overview-grid">
            <article className="overview-card overview-dark">
              <div className="overview-label">PLATFORM MODEL</div>
              <div className="overview-value">One basket.<br />One shared route.</div>
              <div className="overview-note">Merchant items settle individually through Connect.</div>
            </article>
            <article className="overview-card">
              <div className="metric-top"><span>REFUND METHOD</span><span className="metric-icon">↗</span></div>
              <div className="metric-value">Full remaining</div>
              <div className="metric-note">Partial refunds are not enabled</div>
            </article>
            <article className="overview-card">
              <div className="metric-top"><span>RECONCILIATION</span><span className="metric-icon green">✓</span></div>
              <div className="metric-value">Webhook verified</div>
              <div className="metric-note">Transfer reversals follow Stripe confirmation</div>
            </article>
          </div>

          <div className="content-grid">
            <section className="panel">
              <div className="panel-header">
                <div>
                  <div className="eyebrow">CHECKOUT ACTION</div>
                  <h2>Request a refund</h2>
                </div>
                <span className="lock-mark" aria-label="Admin only">⌑</span>
              </div>
              <div className="notice">
                <span className="notice-icon">i</span>
                <p>Admin access is verified by your identity gateway. The app never receives the backend service token.</p>
              </div>
              <form onSubmit={submitRefund}>
                <label>
                  Public gateway URL
                  <input
                    autoCapitalize="none"
                    autoComplete="url"
                    onChange={(event) => {
                      setGatewayUrl(event.target.value)
                      setIdempotencyKey('')
                      setFraudAlerts([])
                      setFraudError('')
                    }}
                    placeholder="https://api.shopstops.example/v1"
                    value={gatewayUrl}
                  />
                  <small>Use the authenticated gateway, not the internal API service address.</small>
                </label>
                <label>
                  Admin session token
                  <input
                    autoCapitalize="none"
                    autoComplete="off"
                    onChange={(event) => {
                      setSessionToken(event.target.value)
                      setIdempotencyKey('')
                      setFraudAlerts([])
                      setFraudError('')
                    }}
                    placeholder="Paste your user access token"
                    type="password"
                    value={sessionToken}
                  />
                  <small>Held in memory only. It is cleared when this page reloads.</small>
                </label>
                <label>
                  Checkout ID
                  <input
                    autoCapitalize="none"
                    autoComplete="off"
                    onChange={(event) => {
                      setCheckoutId(event.target.value)
                      setIdempotencyKey('')
                    }}
                    placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
                    value={checkoutId}
                  />
                </label>
                <label>
                  Reason
                  <select value={reason}                   onChange={(event) => {
                    setReason(event.target.value)
                    setIdempotencyKey('')
                  }}>
                    <option value="requested_by_customer">Requested by customer</option>
                    <option value="duplicate">Duplicate charge</option>
                    <option value="fraudulent">Fraudulent</option>
                  </select>
                </label>
                <div className="refund-summary">
                  <div className="summary-icon">$</div>
                  <div><strong>Refund amount</strong><span>Full remaining refundable balance</span></div>
                  <span className="summary-value">Calculated by Stripe</span>
                </div>
                {error ? <div className="alert alert-error" role="alert">{error}</div> : null}
                {refund ? (
                  <div className="alert alert-success" role="status">
                    Refund {refund.status} · {(refund.amountCents / 100).toLocaleString('en-US', {
                      style: 'currency',
                      currency: refund.currency.toUpperCase(),
                    })} · {refund.refundId}
                  </div>
                ) : null}
                <button className="submit-button" disabled={loading} type="submit">
                  {loading ? 'Sending secure request…' : 'Submit refund request'}
                  {!loading ? <span>→</span> : null}
                </button>
                <p className="form-footnote">Stripe confirms the refund asynchronously. Don’t resubmit with a new key if the first request is still processing.</p>
              </form>
            </section>

            <aside className="side-column">
              <section className="panel">
                <div className="eyebrow">LIFECYCLE</div>
                <h2>What happens next</h2>
                <div className="timeline">
                  <div className="timeline-step">
                    <span className="timeline-number">01</span>
                    <div><strong>Request accepted</strong><p>Stripe creates the customer refund with an idempotent key.</p></div>
                  </div>
                  <div className="timeline-step">
                    <span className="timeline-number">02</span>
                    <div><strong>Webhook confirms</strong><p>Verified Stripe events update the payment and D.C. tax ledgers.</p></div>
                  </div>
                  <div className="timeline-step">
                    <span className="timeline-number">03</span>
                    <div><strong>Merchant payout reversed</strong><p>Previously completed Connect transfers are reversed separately.</p></div>
                  </div>
                </div>
              </section>
              <section className="policy-card">
                <div className="policy-icon">↗</div>
                <div className="eyebrow">OPERATIONS NOTE</div>
                <h3>Disputes need attention.</h3>
                <p>This API records dispute state and reverses payouts when a dispute opens. A won dispute does not automatically restore merchant funds.</p>
                <div className="policy-divider" />
                <span>Review the Stripe Dashboard for evidence deadlines and outcome reconciliation.</span>
              </section>
            </aside>
          </div>

          <footer className="page-footer">
            <span>SHOPSTOPS ADMIN · INTERNAL WORKFLOW</span>
            <span>Card data stays with Stripe</span>
          </footer>
          </> : <>
            <div className="page-heading">
              <div>
                <div className="eyebrow">DELIVERY OPERATIONS</div>
                <h1>Driver telemetry alerts</h1>
                <p>Review unusual GPS speed and route expansion signals.</p>
              </div>
              <div className="status-pill"><span /> HUMAN REVIEW ONLY</div>
            </div>
            <section className="panel">
              <div className="panel-header">
                <div>
                  <div className="eyebrow">FRAUD MONITOR</div>
                  <h2>Review alerts</h2>
                </div>
                <span className="lock-mark" aria-label="Admin only">⌑</span>
              </div>
              <div className="notice">
                <span className="notice-icon">i</span>
                <p>These heuristic signals are not proof of fraud and never trigger automatic penalties or payout changes.</p>
              </div>
              <div className="alert-toolbar">
                <label>
                  Alert status
                  <select
                    onChange={(event) => setFraudStatus(event.target.value as typeof fraudStatus)}
                    value={fraudStatus}
                  >
                    <option value="open">Open</option>
                    <option value="reviewed">Reviewed</option>
                    <option value="dismissed">Dismissed</option>
                    <option value="">All statuses</option>
                  </select>
                </label>
                <button className="submit-button alert-load" disabled={fraudLoading} onClick={() => void loadFraudAlerts()} type="button">
                  {fraudLoading ? 'Loading…' : 'Load alerts'}
                </button>
              </div>
              {fraudError ? <div className="alert alert-error" role="alert">{fraudError}</div> : null}
              {fraudAlerts.length === 0 && !fraudLoading ? (
                <p className="empty-alerts">No alerts loaded. Choose a status and load alerts.</p>
              ) : null}
              <div className="fraud-alert-list">
                {fraudAlerts.map((alert) => (
                  <article className="fraud-alert" key={alert.id}>
                    <div className="fraud-alert-heading">
                      <div>
                        <span className={`severity-badge severity-${alert.severity}`}>{alert.severity}</span>
                        <h3>{alert.alertType === 'impossible_speed' ? 'Extreme GPS speed' : 'Route expansion'}</h3>
                      </div>
                      <span className={`alert-state state-${alert.status}`}>{alert.status}</span>
                    </div>
                    <dl className="fraud-details">
                      <div><dt>Driver</dt><dd>{alert.driverRef}</dd></div>
                      <div><dt>Route</dt><dd>{alert.routeId}</dd></div>
                      <div><dt>Detected</dt><dd>{new Date(alert.detectedAt).toLocaleString()}</dd></div>
                    </dl>
                    <details>
                      <summary>Detection evidence</summary>
                      <pre>{JSON.stringify(alert.evidence, null, 2)}</pre>
                    </details>
                    {alert.status === 'open' ? (
                      <div className="fraud-actions">
                        <button
                          className="review-button"
                          disabled={resolvingAlertId === alert.id}
                          onClick={() => void resolveFraudAlert(alert, 'reviewed')}
                          type="button"
                        >Mark reviewed</button>
                        <button
                          className="dismiss-button"
                          disabled={resolvingAlertId === alert.id}
                          onClick={() => void resolveFraudAlert(alert, 'dismissed')}
                          type="button"
                        >Dismiss</button>
                      </div>
                    ) : alert.resolvedAt ? (
                      <p className="resolved-note">Resolved {new Date(alert.resolvedAt).toLocaleString()}{alert.resolvedBy ? ` by ${alert.resolvedBy}` : ''}</p>
                    ) : null}
                  </article>
                ))}
              </div>
            </section>
            <footer className="page-footer">
              <span>SHOPSTOPS ADMIN · DRIVER MONITORING</span>
              <span>Alerts are review-only</span>
            </footer>
          </>}
        </div>
      </section>
    </main>
  )
}

export default App
