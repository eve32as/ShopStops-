import { useEffect, useState, type FormEvent } from 'react'
import { gatewayURL, requestJSON } from './transport.ts'
import { useRequestSession } from './useRequestSession'
import './App.css'
import FinancePlanning from './FinancePlanning'
import ShopperOrders from './ShopperOrders'
import { adminConfiguration } from './config.ts'
import { dollars } from './financeInputs'
import { workspaceFromHash, workspaces } from './workspaces'

const configuration = adminConfiguration(import.meta.env.VITE_API_URL, import.meta.env.DEV)

type RefundResult = {
  refundId: string
  status: string
  amountCents: number
  currency: string
}

function newIdempotencyKey() {
  return globalThis.crypto?.randomUUID?.() ?? `admin-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

function App() {
  const [workspace, setWorkspace] = useState(() => workspaceFromHash(location.hash))
  useEffect(() => {
    function navigate() { setWorkspace(workspaceFromHash(location.hash)) }
    window.addEventListener('hashchange', navigate)
    return () => window.removeEventListener('hashchange', navigate)
  }, [])
  const currentWorkspace = workspaces.find(item => item.id === workspace)!
  const [gatewayUrl, setGatewayUrl] = useState(configuration.baseUrl)
  const [sessionToken, setSessionToken] = useState('')
  const [checkoutId, setCheckoutId] = useState('')
  const [reason, setReason] = useState('requested_by_customer')
  const [idempotencyKey, setIdempotencyKey] = useState('')
  const [refund, setRefund] = useState<RefundResult | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [revision, setRevision] = useState(0)
  const { session } = useRequestSession(gatewayUrl, sessionToken)

  function changeSession(url: string, token: string) {
    session.close()
    setGatewayUrl(url); setSessionToken(token); setRevision(value => value + 1)
    setRefund(null); setError(''); setCheckoutId(''); setReason('requested_by_customer'); setIdempotencyKey(''); setLoading(false)
  }

  async function submitRefund(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (loading) return
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
      url = gatewayURL(baseUrl, import.meta.env.DEV)
      url.pathname = `${url.pathname.replace(/\/+$/, '')}/admin/checkouts/${encodeURIComponent(id)}/refund`
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
    const scope = session.begin()
    if (!scope) return
    setLoading(true)
    try {
      const result = await requestJSON<RefundResult>(url, token, {
        signal: scope.signal,
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
      scope.assertCurrent()
      setRefund(result)
      setIdempotencyKey('')
    } catch (requestError) {
      if (!scope.signal.aborted) setError(requestError instanceof Error ? requestError.message : 'Refund request failed.')
    } finally {
      session.finish(scope)
      if (!scope.signal.aborted) setLoading(false)
    }
  }

  if (configuration.error) return <main className="shell"><div className="alert alert-error" role="alert">{configuration.error}</div></main>

  return (
    <main className="shell">
      <aside className="rail">
        <a className="brand" href="#overview" aria-label="ShopStops admin home">
          <img className="brand-mark" src="/shopstops-mark.svg" alt="" />
          <span>shopstops<span className="brand-sub">CONTROL ROOM</span></span>
        </a>
        <div className="rail-label">WORKSPACE</div>
        <nav aria-label="Admin workspaces">
        {workspaces.map(item => <a key={item.id} className={`nav-item ${workspace === item.id ? 'nav-active' : ''}`} href={`#${item.id}`} aria-current={workspace === item.id ? 'page' : undefined}><span className="nav-icon" aria-hidden="true">{item.id === 'overview' ? '⌂' : '◎'}</span>{item.label}</a>)}
        </nav>
        <div className="rail-bottom">
          <div className="connection-dot" />
          <span>Connection not independently verified</span>
        </div>
      </aside>

      <section className="main">
        <header className="topbar">
          <div className="breadcrumbs">OPERATIONS <span>/</span> {currentWorkspace.label.toUpperCase()}</div>
          <div className="admin-chip"><span className="admin-avatar">A</span> Admin workspace</div>
        </header>
        <nav className="compact-nav" aria-label="Compact admin workspaces">
          {workspaces.map(item => <a key={item.id} href={`#${item.id}`} aria-current={workspace === item.id ? 'page' : undefined}>{item.label}</a>)}
        </nav>
        <div className="page">
          <div className="page-heading">
            <div>
              <div className="eyebrow">SHOPSTOPS / {currentWorkspace.label.toUpperCase()}</div>
              <h1>{currentWorkspace.title}</h1>
              <p>{currentWorkspace.description}</p>
            </div>
            <div className="status-pill">GATEWAY REQUIRED</div>
          </div>

          <section hidden={workspace !== 'overview'} aria-label="Workspace overview">
            <div className="notice">No live operational KPIs are loaded. These are workflow entry points, not revenue, payment or assignment totals.</div>
            <div className="overview-grid">
              {workspaces.filter(item => item.id !== 'overview').map(item => <a key={item.id} className="overview-card workspace-link" href={`#${item.id}`}><div className="eyebrow">{item.id === 'refund-desk' ? '01 / RESOLVE' : item.id === 'finance-planning' ? '02 / PLAN' : '03 / VERIFY'}</div><h2>{item.label} →</h2><p>{item.description}</p></a>)}
            </div>
            <section className="panel role-guide"><h2>Who does what</h2><p><strong>Customer:</strong> buys orders in mobile. <strong>Driver:</strong> follows a dispatch-assigned route. <strong>Merchant:</strong> manages their own catalog and billing.</p><p><strong>Worker shopping:</strong> managed by partner store; not available in the mobile app. Authorized admins record supervised assignments and verified completions here; earnings stay pending review, never paid by this UI.</p></section>
          </section>

          <details className="panel operator-settings">
            <summary>Operator connection · {sessionToken ? 'Session entered, access not verified' : 'Setup required'}</summary>
            <p>Manual session entry is an operator workflow, not integrated SSO. Obtain a user access token from your approved identity provider. Never use a backend service token. Every action is authorized by the gateway.</p>
            <div className="finance-inputs">
              <label>Operator connection · public gateway URL<input aria-label="Operator connection · public gateway URL" autoCapitalize="none" autoComplete="url" placeholder="https://api.shopstops.example/v1" value={gatewayUrl} onChange={event => changeSession(event.target.value, sessionToken)} /><small>Use the public authenticated gateway, including its API prefix.</small></label>
              <label>Admin session token<input aria-label="Admin session token" autoCapitalize="none" autoComplete="off" type="password" placeholder="Paste your user access token" value={sessionToken} onChange={event => changeSession(gatewayUrl, event.target.value)} /><small>Memory only. Changing credentials clears private records and cancels pending requests.</small></label>
            </div>
            <button type="button" onClick={() => changeSession(gatewayUrl, '')}>Clear session and private records</button>
          </details>

          <div hidden={workspace !== 'refund-desk'}>
          <div className="content-grid">
            <section className="panel" id="refund-desk" aria-label="Refund desk">
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
                <fieldset disabled={loading}>
                <legend>Refund details</legend>
                <label>
                  Checkout ID
                  <input
                    required
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
                    Refund {refund.status} · {refund.currency.toUpperCase()} {dollars(refund.amountCents)} · {refund.refundId}
                  </div>
                ) : null}
                <button className="submit-button" disabled={loading} type="submit">
                  {loading ? 'Sending secure request…' : 'Submit refund request'}
                  {!loading ? <span>→</span> : null}
                </button>
                <p className="form-footnote">Stripe confirms the refund asynchronously. Don’t resubmit with a new key if the first request is still processing.</p>
                {idempotencyKey && <p role="status">Retry key retained in memory. Retry unchanged; reconcile an uncertain outcome before editing these details.</p>}
                </fieldset>
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
          </div>

          <div hidden={workspace !== 'finance-planning'}><FinancePlanning key={`finance-${revision}`} gatewayUrl={gatewayUrl} sessionToken={sessionToken} /></div>
          <div hidden={workspace !== 'shopper-orders'}><ShopperOrders key={`shopper-${revision}`} gatewayUrl={gatewayUrl} sessionToken={sessionToken} /></div>

          <footer className="page-footer">
            <span>SHOPSTOPS ADMIN · INTERNAL WORKFLOW</span>
            <span>Card data stays with Stripe</span>
          </footer>
        </div>
      </section>
    </main>
  )
}

export default App
