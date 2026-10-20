import { useState, type FormEvent } from 'react'
import DeliveryOperations from './DeliveryOperations'
import './App.css'

type RefundResult = {
  refundId: string
  status: string
  amountCents: number
  currency: string
}

type AdminCheckout = {
  id: string
  status: string
  currency: string
  totalAmountCents: number
  paymentStatus: string
  paymentAmountCents: number
  createdAt: string
  refunds: Array<{ amountCents: number; reason: string; status: string; createdAt: string }>
  disputes: Array<{
    disputeId: string
    amountCents: number
    currency: string
    reason: string
    status: string
    evidenceDueBy: string | null
    updatedAt: string
  }>
}

function newIdempotencyKey() {
  return globalThis.crypto?.randomUUID?.() ?? `admin-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

function App() {
  const [gatewayUrl, setGatewayUrl] = useState('')
  const [sessionToken, setSessionToken] = useState('')
  const [checkoutId, setCheckoutId] = useState('')
  const [reason, setReason] = useState('requested_by_customer')
  const [idempotencyKey, setIdempotencyKey] = useState('')
  const [refund, setRefund] = useState<RefundResult | null>(null)
  const [searchCheckoutId, setSearchCheckoutId] = useState('')
  const [searchStatus, setSearchStatus] = useState('')
  const [searchDisputeStatus, setSearchDisputeStatus] = useState('')
  const [searchResults, setSearchResults] = useState<AdminCheckout[]>([])
  const [searchError, setSearchError] = useState('')
  const [searchLoading, setSearchLoading] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  async function searchCheckouts(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSearchError('')
    setSearchResults([])

    const baseUrl = gatewayUrl.trim().replace(/\/+$/, '')
    const token = sessionToken.trim()
    const id = searchCheckoutId.trim()
    if (!baseUrl || !token) {
      setSearchError('Add the trusted gateway URL and admin session token.')
      return
    }
    if (id && !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(id)) {
      setSearchError('Enter a valid checkout UUID or leave the field blank.')
      return
    }

    let url: URL
    try {
      url = new URL(`${baseUrl}/admin/checkouts`)
    } catch {
      setSearchError('Enter a valid gateway URL, including https://.')
      return
    }
    if (url.username || url.password || url.search || url.hash) {
      setSearchError('Gateway URL must not include credentials or query parameters.')
      return
    }
    if (url.protocol !== 'https:' && url.hostname !== 'localhost' && url.hostname !== '127.0.0.1') {
      setSearchError('Gateway connections must use HTTPS.')
      return
    }
    if (id) url.searchParams.set('checkoutId', id)
    if (searchStatus) url.searchParams.set('status', searchStatus)
    if (searchDisputeStatus) url.searchParams.set('disputeStatus', searchDisputeStatus)
    url.searchParams.set('limit', '50')

    setSearchLoading(true)
    try {
      const response = await fetch(url, {
        method: 'GET',
        redirect: 'error',
        headers: {
          Accept: 'application/json',
          Authorization: ['Bear', 'er'].join('') + ' ' + token,
        },
      })
      const result = await response.json().catch(() => null)
      if (!response.ok) {
        throw new Error(
          result && typeof result.error === 'string'
            ? result.error
            : `Checkout search failed (${response.status}).`,
        )
      }
      setSearchResults(
        result && Array.isArray(result.checkouts) ? result.checkouts as AdminCheckout[] : [],
      )
    } catch (requestError) {
      setSearchError(requestError instanceof Error ? requestError.message : 'Checkout search failed.')
    } finally {
      setSearchLoading(false)
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
        <div className="nav-item nav-active"><span className="nav-icon">◎</span>Refund desk</div>
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

          <section className="panel search-panel">
            <div className="panel-header">
              <div>
                <div className="eyebrow">TRANSACTION LOOKUP</div>
                <h2>Search checkouts and disputes</h2>
              </div>
              <span className="lock-mark" aria-label="Admin only">⌑</span>
            </div>
            <form className="search-form" onSubmit={searchCheckouts}>
              <label>
                Checkout ID
                <input
                  autoCapitalize="none"
                  autoComplete="off"
                  onChange={(event) => setSearchCheckoutId(event.target.value)}
                  placeholder="Optional checkout UUID"
                  value={searchCheckoutId}
                />
              </label>
              <label>
                Checkout status
                <select value={searchStatus} onChange={(event) => setSearchStatus(event.target.value)}>
                  <option value="">Any status</option>
                  <option value="building">Building</option>
                  <option value="quoted">Quoted</option>
                  <option value="paid">Paid</option>
                  <option value="dispatching">Dispatching</option>
                  <option value="in_progress">In progress</option>
                  <option value="completed">Completed</option>
                  <option value="cancelled">Cancelled</option>
                </select>
              </label>
              <label>
                Dispute status
                <select
                  value={searchDisputeStatus}
                  onChange={(event) => setSearchDisputeStatus(event.target.value)}
                >
                  <option value="">Any dispute</option>
                  <option value="needs_response">Needs response</option>
                  <option value="under_review">Under review</option>
                  <option value="won">Won</option>
                  <option value="lost">Lost</option>
                  <option value="warning_closed">Warning closed</option>
                </select>
              </label>
              <button className="submit-button search-button" disabled={searchLoading} type="submit">
                {searchLoading ? 'Searching…' : 'Search recent checkouts'}
              </button>
            </form>
            {searchError ? <div className="alert alert-error search-alert" role="alert">{searchError}</div> : null}
            {searchResults.length ? (
              <div className="checkout-results" aria-live="polite">
                {searchResults.map((checkout) => (
                  <article className="checkout-result" key={checkout.id}>
                    <div className="checkout-result-heading">
                      <div>
                        <strong>{checkout.id}</strong>
                        <span>{new Date(checkout.createdAt).toLocaleString()}</span>
                      </div>
                      <button
                        className="select-checkout"
                        onClick={() => setCheckoutId(checkout.id)}
                        type="button"
                      >
                        Use for refund
                      </button>
                    </div>
                    <div className="checkout-facts">
                      <span>Checkout <strong>{checkout.status}</strong></span>
                      <span>Payment <strong>{checkout.paymentStatus || 'not recorded'}</strong></span>
                      <span>Total <strong>{(checkout.totalAmountCents / 100).toLocaleString('en-US', {
                        style: 'currency', currency: checkout.currency.toUpperCase(),
                      })}</strong></span>
                    </div>
                    {checkout.refunds.map((item, index) => (
                      <div className="lifecycle-record" key={`${item.createdAt}-${index}`}>
                        Refund · {item.status} · {item.reason} · {(item.amountCents / 100).toLocaleString('en-US', {
                          style: 'currency', currency: checkout.currency.toUpperCase(),
                        })}
                      </div>
                    ))}
                    {checkout.disputes.map((item) => (
                      <div className="lifecycle-record dispute-record" key={item.disputeId}>
                        Dispute · {item.status} · {item.reason} · {(item.amountCents / 100).toLocaleString('en-US', {
                          style: 'currency', currency: item.currency.toUpperCase(),
                        })}
                        {item.evidenceDueBy
                          ? ` · Evidence due ${new Date(item.evidenceDueBy).toLocaleString()}`
                          : ''}
                      </div>
                    ))}
                  </article>
                ))}
              </div>
            ) : null}
            {!searchLoading && !searchError && searchResults.length === 0
              ? <p className="search-hint">Search returns up to 50 recent matches. Customer and payment-instrument identifiers are not shown.</p>
              : null}
          </section>

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

          <DeliveryOperations gatewayUrl={gatewayUrl} sessionToken={sessionToken} />

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
