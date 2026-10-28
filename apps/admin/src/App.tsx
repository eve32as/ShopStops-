import { useCallback, useEffect, useState, type FormEvent } from 'react'
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

type DriverProfile = {
  driverRef: string
  displayName: string
  status: 'pending' | 'active' | 'suspended'
  createdAt: string
  updatedAt: string
  verifiedAt?: string
}

type NotificationAttempt = {
  status: string
  errorCode?: string
  providerMessageId?: string
  startedAt: string
  completedAt?: string
}

type NotificationLog = {
  id: string
  recipientRef: string
  notificationType: string
  channel: string
  status: 'queued' | 'sending' | 'sent' | 'failed'
  attempts: number
  lastError?: string
  providerMessageId?: string
  retryRequestedBy?: string
  createdAt: string
  sentAt?: string
  retryRequestedAt?: string
  attemptHistory: NotificationAttempt[]
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

  const headers = new Headers(options.headers)
  headers.set('Accept', 'application/json')
  headers.set('Authorization', ['Bear', 'er'].join('') + ' ' + token)
  if (options.body) headers.set('Content-Type', 'application/json')
  const response = await fetch(url, { ...options, redirect: 'error', headers })
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
  const [activeView, setActiveView] = useState<'refunds' | 'driver-alerts' | 'dispatch' | 'notifications'>('refunds')
  const [fraudAlerts, setFraudAlerts] = useState<FraudAlert[]>([])
  const [fraudStatus, setFraudStatus] = useState<'open' | 'reviewed' | 'dismissed' | ''>('open')
  const [fraudError, setFraudError] = useState('')
  const [fraudLoading, setFraudLoading] = useState(false)
  const [resolvingAlertId, setResolvingAlertId] = useState('')
  const [notificationLogs, setNotificationLogs] = useState<NotificationLog[]>([])
  const [notificationStatus, setNotificationStatus] = useState<'' | NotificationLog['status']>('')
  const [notificationError, setNotificationError] = useState('')
  const [notificationLoading, setNotificationLoading] = useState(false)
  const [retryingNotificationId, setRetryingNotificationId] = useState('')
  const [payoutDriverRef, setPayoutDriverRef] = useState('')
  const [payoutEmail, setPayoutEmail] = useState('')
  const [profileSaved, setProfileSaved] = useState('')
  const [assignmentRouteId, setAssignmentRouteId] = useState('')
  const [assignmentDriverRef, setAssignmentDriverRef] = useState('')
  const [assignmentMessage, setAssignmentMessage] = useState('')
  const [assignmentError, setAssignmentError] = useState('')
  const [assignmentLoading, setAssignmentLoading] = useState(false)
  const [driverProfiles, setDriverProfiles] = useState<DriverProfile[]>([])
  const [driverDirectoryLoading, setDriverDirectoryLoading] = useState(false)
  const [driverProfileRef, setDriverProfileRef] = useState('')
  const [driverProfileName, setDriverProfileName] = useState('')
  const [driverProfileStatus, setDriverProfileStatus] = useState<DriverProfile['status']>('pending')
  const [driverDirectoryError, setDriverDirectoryError] = useState('')
  const [driverDirectoryMessage, setDriverDirectoryMessage] = useState('')
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
          ? { ...item, status }
          : item,
      ))
    } catch (requestError) {
      setFraudError(requestError instanceof Error ? requestError.message : 'Could not resolve driver alert.')
    } finally {
      setResolvingAlertId('')
    }
  }

  const loadNotificationLogs = useCallback(async () => {
    setNotificationError('')
    setNotificationLoading(true)
    try {
      const query = new URLSearchParams({ limit: '100' })
      if (notificationStatus) query.set('status', notificationStatus)
      const logs = await fraudRequest<NotificationLog[]>(
        gatewayUrl, sessionToken, `admin/notifications?${query.toString()}`,
      )
      setNotificationLogs(logs)
    } catch (requestError) {
      setNotificationError(requestError instanceof Error
        ? requestError.message
        : 'Could not load notification logs.')
    } finally {
      setNotificationLoading(false)
    }
  }, [gatewayUrl, sessionToken, notificationStatus])

  async function retryNotification(notification: NotificationLog) {
    setNotificationError('')
    setRetryingNotificationId(notification.id)
    try {
      await fraudRequest<{ id: string; status: string }>(
        gatewayUrl, sessionToken,
        `admin/notifications/${encodeURIComponent(notification.id)}/retry`,
        { method: 'POST' },
      )
      await loadNotificationLogs()
    } catch (requestError) {
      setNotificationError(requestError instanceof Error
        ? requestError.message
        : 'Could not queue notification retry.')
    } finally {
      setRetryingNotificationId('')
    }
  }

  async function savePayoutProfile(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setNotificationError('')
    setProfileSaved('')
    try {
      await fraudRequest<{ driverRef: string; status: string }>(
        gatewayUrl,
        sessionToken,
        `admin/driver-payout-profiles/${encodeURIComponent(payoutDriverRef.trim())}`,
        { method: 'PUT', body: JSON.stringify({ email: payoutEmail.trim() }) },
      )
      setProfileSaved(`Weekly statement email enabled for ${payoutDriverRef.trim()}.`)
      setPayoutEmail('')
    } catch (requestError) {
      setNotificationError(requestError instanceof Error
        ? requestError.message
        : 'Could not save courier email.')
    }
  }

  async function assignDriver(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setAssignmentMessage('')
    setAssignmentError('')
    const routeId = assignmentRouteId.trim()
    const driverRef = assignmentDriverRef.trim()
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(routeId)) {
      setAssignmentError('Enter a valid route UUID.')
      return
    }
    if (!driverRef || driverRef.length > 200) {
      setAssignmentError('Enter a valid driver identity (1–200 characters).')
      return
    }
    setAssignmentLoading(true)
    try {
      await fraudRequest<{ routeId: string; driverRef: string; status: string }>(
        gatewayUrl,
        sessionToken,
        `dispatch/routes/${encodeURIComponent(routeId)}/assign`,
        { method: 'POST', body: JSON.stringify({ driverRef }) },
      )
      setAssignmentMessage(`Route ${routeId} assigned to ${driverRef}.`)
    } catch (requestError) {
      setAssignmentError(requestError instanceof Error
        ? requestError.message
        : 'Could not assign this route.')
    } finally {
      setAssignmentLoading(false)
    }
  }

  async function loadDriverProfiles() {
    setDriverDirectoryError('')
    setDriverDirectoryLoading(true)
    try {
      const profiles = await fraudRequest<DriverProfile[]>(
        gatewayUrl, sessionToken, 'admin/drivers?limit=100',
      )
      setDriverProfiles(profiles)
    } catch (requestError) {
      setDriverDirectoryError(requestError instanceof Error
        ? requestError.message
        : 'Could not load driver profiles.')
    } finally {
      setDriverDirectoryLoading(false)
    }
  }

  async function saveDriverProfile(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setDriverDirectoryError('')
    setDriverDirectoryMessage('')
    const driverRef = driverProfileRef.trim()
    const displayName = driverProfileName.trim()
    if (!driverRef || driverRef.length > 200 || !displayName || displayName.length > 120) {
      setDriverDirectoryError('Enter an identity reference (1–200 characters) and display name (1–120 characters).')
      return
    }
    setDriverDirectoryLoading(true)
    try {
      const profile = await fraudRequest<DriverProfile>(
        gatewayUrl,
        sessionToken,
        `admin/drivers/${encodeURIComponent(driverRef)}`,
        {
          method: 'PUT',
          body: JSON.stringify({ displayName, status: driverProfileStatus }),
        },
      )
      setDriverDirectoryMessage(
        `${profile.displayName} profile saved as ${profile.status}. Active status indicates the driver has been verified for dispatch.`,
      )
      await loadDriverProfiles()
    } catch (requestError) {
      setDriverDirectoryError(requestError instanceof Error
        ? requestError.message
        : 'Could not save driver profile.')
    } finally {
      setDriverDirectoryLoading(false)
    }
  }

  useEffect(() => {
    if (activeView !== 'notifications' || !gatewayUrl.trim() || !sessionToken.trim()) return
    const initialLoad = window.setTimeout(() => void loadNotificationLogs(), 0)
    const timer = window.setInterval(() => void loadNotificationLogs(), 10_000)
    return () => {
      window.clearTimeout(initialLoad)
      window.clearInterval(timer)
    }
  }, [activeView, gatewayUrl, sessionToken, loadNotificationLogs])

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
        <button
          className={`nav-item ${activeView === 'dispatch' ? 'nav-active' : 'nav-muted'}`}
          onClick={() => setActiveView('dispatch')}
          type="button"
        ><span className="nav-icon">↗</span>Route assignment</button>
        <button
          className={`nav-item ${activeView === 'notifications' ? 'nav-active' : 'nav-muted'}`}
          onClick={() => setActiveView('notifications')}
          type="button"
        ><span className="nav-icon">✉</span>Notifications</button>
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
                      setNotificationLogs([])
                      setNotificationError('')
                      setProfileSaved('')
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
                      setNotificationLogs([])
                      setNotificationError('')
                      setProfileSaved('')
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
          </> : activeView === 'driver-alerts' ? <>
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
          </> : activeView === 'dispatch' ? <>
            <div className="page-heading">
              <div>
                <div className="eyebrow">DELIVERY OPERATIONS</div>
                <h1>Assign a driver</h1>
                <p>Assign a routed, unstarted delivery to an authenticated driver identity.</p>
              </div>
              <div className="status-pill"><span /> DISPATCH CONTROL</div>
            </div>
            <section className="panel">
              <div className="panel-header">
                <div>
                  <div className="eyebrow">ROUTE HANDOFF</div>
                  <h2>Dispatch a delivery</h2>
                </div>
                <span className="lock-mark" aria-label="Dispatch or admin only">⌑</span>
              </div>
              <div className="notice">
                <span className="notice-icon">i</span>
                <p>Only active, admin-verified driver profiles can receive routed, unstarted deliveries. A driver can have one active route; assignment changes are locked after handoff.</p>
              </div>
              <div className="alert-toolbar">
                <span>{driverProfiles.filter((driver) => driver.status === 'active').length} eligible driver profiles loaded</span>
                <button className="submit-button alert-load" disabled={driverDirectoryLoading}
                  onClick={() => void loadDriverProfiles()} type="button">
                  {driverDirectoryLoading ? 'Loading…' : 'Load driver directory'}
                </button>
              </div>
              {driverDirectoryError ? <div className="alert alert-error" role="alert">{driverDirectoryError}</div> : null}
              {driverDirectoryMessage ? <div className="alert alert-success" role="status">{driverDirectoryMessage}</div> : null}
              <form onSubmit={(event) => void saveDriverProfile(event)}>
                <label>
                  Driver identity reference
                  <input autoComplete="off" value={driverProfileRef}
                    onChange={(event) => setDriverProfileRef(event.target.value)}
                    placeholder="Exact identity-provider subject" required />
                </label>
                <label>
                  Driver display name
                  <input autoComplete="name" value={driverProfileName}
                    onChange={(event) => setDriverProfileName(event.target.value)}
                    placeholder="Name for operations" required />
                </label>
                <label>
                  Verification status
                  <select value={driverProfileStatus}
                    onChange={(event) => setDriverProfileStatus(event.target.value as DriverProfile['status'])}>
                    <option value="pending">Pending review</option>
                    <option value="active">Active · verified for dispatch</option>
                    <option value="suspended">Suspended</option>
                  </select>
                </label>
                <button className="submit-button" disabled={driverDirectoryLoading} type="submit">
                  {driverDirectoryLoading ? 'Saving…' : 'Save driver profile'}
                </button>
              </form>
              {driverProfiles.length > 0 ? (
                <div className="fraud-alert-list">
                  {driverProfiles.map((driver) => (
                    <article className="fraud-alert" key={driver.driverRef}>
                      <div className="fraud-alert-heading">
                        <div><h3>{driver.displayName}</h3><span>{driver.driverRef}</span></div>
                        <span className={`alert-state state-${driver.status === 'active' ? 'reviewed' : driver.status}`}>
                          {driver.status}
                        </span>
                      </div>
                      <p className="resolved-note">
                        {driver.verifiedAt ? `Verified ${new Date(driver.verifiedAt).toLocaleString()}` : 'Not verified for dispatch'}
                      </p>
                    </article>
                  ))}
                </div>
              ) : null}
              <form onSubmit={(event) => void assignDriver(event)}>
                <label>
                  Route ID
                  <input autoComplete="off" value={assignmentRouteId}
                    onChange={(event) => setAssignmentRouteId(event.target.value)}
                    placeholder="Route UUID from dispatch" required />
                </label>
                <label>
                  Eligible driver
                  <select required value={assignmentDriverRef}
                    onChange={(event) => setAssignmentDriverRef(event.target.value)}>
                    <option value="">Select an active verified driver</option>
                    {driverProfiles.filter((driver) => driver.status === 'active').map((driver) => (
                      <option key={driver.driverRef} value={driver.driverRef}>
                        {driver.displayName} · {driver.driverRef}
                      </option>
                    ))}
                  </select>
                </label>
                <button className="submit-button"
                  disabled={assignmentLoading || !driverProfiles.some((driver) =>
                    driver.driverRef === assignmentDriverRef && driver.status === 'active')}
                  type="submit">
                  {assignmentLoading ? 'Assigning…' : 'Assign route'}
                </button>
              </form>
              {assignmentError ? <div className="alert alert-error" role="alert">{assignmentError}</div> : null}
              {assignmentMessage ? <div className="alert alert-success" role="status">{assignmentMessage}</div> : null}
            </section>
            <footer className="page-footer">
              <span>SHOPSTOPS ADMIN · ROUTE DISPATCH</span>
              <span>Assignment is recorded in the delivery outbox</span>
            </footer>
          </> : <>
            <div className="page-heading">
              <div>
                <div className="eyebrow">COMMUNICATION OPERATIONS</div>
                <h1>Notification delivery</h1>
                <p>Audit courier earnings statements and retry failed email delivery.</p>
              </div>
              <div className="status-pill"><span /> AUTO-REFRESH · 10 SEC</div>
            </div>
            <section className="panel">
              <div className="panel-header">
                <div>
                  <div className="eyebrow">COURIER PAYOUT STATEMENTS</div>
                  <h2>Email recipient setup</h2>
                </div>
                <span className="lock-mark" aria-label="Admin only">⌑</span>
              </div>
              <div className="notice">
                <span className="notice-icon">i</span>
                <p>Statements are generated for completed routes from the previous UTC week. They report earnings and do not initiate or confirm funds transfer.</p>
              </div>
              <form onSubmit={(event) => void savePayoutProfile(event)}>
                <label>
                  Driver reference
                  <input autoComplete="off" value={payoutDriverRef}
                    onChange={(event) => setPayoutDriverRef(event.target.value)}
                    placeholder="driver reference" required />
                </label>
                <label>
                  Courier email
                  <input autoComplete="email" type="email" value={payoutEmail}
                    onChange={(event) => setPayoutEmail(event.target.value)}
                    placeholder="courier@example.com" required />
                </label>
                <button className="submit-button" type="submit">Save statement recipient</button>
              </form>
              {profileSaved ? <div className="alert alert-success" role="status">{profileSaved}</div> : null}
            </section>
            <section className="panel notification-panel">
              <div className="panel-header">
                <div>
                  <div className="eyebrow">DELIVERY MESSAGE LOG</div>
                  <h2>Recent notifications</h2>
                </div>
                <span className="notification-count">{notificationLogs.length} shown</span>
              </div>
              <div className="alert-toolbar">
                <label>
                  Delivery status
                  <select
                    value={notificationStatus}
                    onChange={(event) => setNotificationStatus(event.target.value as typeof notificationStatus)}
                  >
                    <option value="">All statuses</option>
                    <option value="queued">Queued</option>
                    <option value="sending">Sending</option>
                    <option value="sent">Sent</option>
                    <option value="failed">Failed</option>
                  </select>
                </label>
                <button className="submit-button alert-load" disabled={notificationLoading}
                  onClick={() => void loadNotificationLogs()} type="button">
                  {notificationLoading ? 'Refreshing…' : 'Refresh now'}
                </button>
              </div>
              {notificationError ? <div className="alert alert-error" role="alert">{notificationError}</div> : null}
              {notificationLogs.length === 0 && !notificationLoading
                ? <p className="empty-alerts">No matching messages have been logged.</p>
                : null}
              <div className="notification-list">
                {notificationLogs.map((notification) => (
                  <article className="notification-row" key={notification.id}>
                    <div className="notification-row-heading">
                      <div>
                        <span className={`alert-state state-${notification.status}`}>{notification.status}</span>
                        <h3>{notification.notificationType.replaceAll('_', ' ')}</h3>
                      </div>
                      <span className="notification-channel">{notification.channel}</span>
                    </div>
                    <dl className="fraud-details notification-details">
                      <div><dt>Courier</dt><dd>{notification.recipientRef}</dd></div>
                      <div><dt>Attempts</dt><dd>{notification.attempts}</dd></div>
                      <div><dt>Created</dt><dd>{new Date(notification.createdAt).toLocaleString()}</dd></div>
                      {notification.sentAt
                        ? <div><dt>Sent</dt><dd>{new Date(notification.sentAt).toLocaleString()}</dd></div>
                        : null}
                      {notification.retryRequestedAt
                        ? <div><dt>Last retry requested</dt><dd>{new Date(notification.retryRequestedAt).toLocaleString()} by {notification.retryRequestedBy}</dd></div>
                        : null}
                    </dl>
                    {notification.lastError
                      ? <p className="notification-error">Latest error: {notification.lastError}</p>
                      : null}
                    {notification.providerMessageId
                      ? <p className="notification-message-id">Message ID: {notification.providerMessageId}</p>
                      : null}
                    <details>
                      <summary>Attempt history ({notification.attemptHistory.length})</summary>
                      <ol className="attempt-history">
                        {notification.attemptHistory.map((attempt, index) => (
                          <li key={`${notification.id}-${index}`}>
                            <span className={`alert-state state-${attempt.status}`}>{attempt.status}</span>
                            <time>{new Date(attempt.startedAt).toLocaleString()}</time>
                            {attempt.errorCode ? <span>{attempt.errorCode}</span> : null}
                            {attempt.providerMessageId ? <span>{attempt.providerMessageId}</span> : null}
                          </li>
                        ))}
                      </ol>
                    </details>
                    {notification.status === 'failed' ? (
                      <div className="fraud-actions">
                        <button className="review-button"
                          disabled={retryingNotificationId === notification.id}
                          onClick={() => void retryNotification(notification)} type="button">
                          {retryingNotificationId === notification.id ? 'Queueing…' : 'Retry email'}
                        </button>
                      </div>
                    ) : null}
                  </article>
                ))}
              </div>
            </section>
            <footer className="page-footer">
              <span>SHOPSTOPS ADMIN · MESSAGE AUDIT</span>
              <span>Recipient addresses are not exposed in this log</span>
            </footer>
          </>}
        </div>
      </section>
    </main>
  )
}

export default App
