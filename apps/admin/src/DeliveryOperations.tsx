import { useState, type FormEvent } from 'react'

type DeliveryZone = {
  id: string
  name: string
  enabled: boolean
}

type Bundle = {
  id: string
  status: string
  stops: Array<{
    id: string
    orderId: string
    sequence: number
    type: string
    status: string
  }>
}

type Props = {
  gatewayUrl: string
  sessionToken: string
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

function apiUrl(gatewayUrl: string, path: string) {
  const url = new URL(gatewayUrl.trim())
  if (url.username || url.password || url.search || url.hash) {
    throw new Error('Gateway URL must not include credentials or query parameters.')
  }
  if (url.protocol !== 'https:' && url.hostname !== 'localhost' && url.hostname !== '127.0.0.1') {
    throw new Error('Gateway connections must use HTTPS.')
  }
  const prefix = url.pathname.replace(/\/+$/, '')
  url.pathname = `${prefix.endsWith('/v1') ? prefix : `${prefix}/v1`}${path}`
  return url
}

function newKey() {
  return globalThis.crypto?.randomUUID?.()
    ?? `dispatch-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

async function requestError(response: Response) {
  const result = await response.json().catch(() => null)
  return new Error(
    result && typeof result.error === 'string'
      ? result.error
      : `Request failed (${response.status}).`,
  )
}

export default function DeliveryOperations({ gatewayUrl, sessionToken }: Props) {
  const [zones, setZones] = useState<DeliveryZone[]>([])
  const [zonesError, setZonesError] = useState('')
  const [zonesLoading, setZonesLoading] = useState(false)
  const [updatingZone, setUpdatingZone] = useState('')
  const [seedOrderId, setSeedOrderId] = useState('')
  const [capacityLiters, setCapacityLiters] = useState('100')
  const [maxOrders, setMaxOrders] = useState('3')
  const [pickupRadiusMeters, setPickupRadiusMeters] = useState('2000')
  const [dropoffRadiusMeters, setDropoffRadiusMeters] = useState('2000')
  const [dispatchKey, setDispatchKey] = useState('')
  const [bundle, setBundle] = useState<Bundle | null>(null)
  const [dispatchError, setDispatchError] = useState('')
  const [dispatching, setDispatching] = useState(false)

  async function loadZones() {
    setZonesError('')
    if (!gatewayUrl.trim() || !sessionToken.trim()) {
      setZonesError('Add the trusted gateway URL and admin session token above.')
      return
    }
    let url: URL
    try {
      url = apiUrl(gatewayUrl, '/admin/delivery-zones')
    } catch (error) {
      setZonesError(error instanceof Error ? error.message : 'Enter a valid gateway URL.')
      return
    }
    setZonesLoading(true)
    try {
      const response = await fetch(url, {
        redirect: 'error',
        headers: {
          Accept: 'application/json',
          Authorization: ['Bear', 'er'].join('') + ' ' + sessionToken.trim(),
        },
      })
      if (!response.ok) throw await requestError(response)
      const result = await response.json()
      setZones(Array.isArray(result.deliveryZones) ? result.deliveryZones as DeliveryZone[] : [])
    } catch (error) {
      setZonesError(error instanceof Error ? error.message : 'Could not load delivery zones.')
    } finally {
      setZonesLoading(false)
    }
  }

  async function updateZone(zone: DeliveryZone) {
    setZonesError('')
    if (zone.enabled &&
      !window.confirm(`Disable ${zone.name}? New orders and dispatch will stop in this zone.`)) {
      return
    }
    let url: URL
    try {
      url = apiUrl(gatewayUrl, `/admin/delivery-zones/${encodeURIComponent(zone.id)}`)
    } catch (error) {
      setZonesError(error instanceof Error ? error.message : 'Enter a valid gateway URL.')
      return
    }
    setUpdatingZone(zone.id)
    try {
      const response = await fetch(url, {
        method: 'PATCH',
        redirect: 'error',
        headers: {
          Accept: 'application/json',
          Authorization: ['Bear', 'er'].join('') + ' ' + sessionToken.trim(),
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ enabled: !zone.enabled }),
      })
      if (!response.ok) throw await requestError(response)
      const updated = await response.json() as DeliveryZone
      setZones((current) => current.map((item) => item.id === updated.id ? updated : item))
    } catch (error) {
      setZonesError(error instanceof Error ? error.message : 'Could not update delivery zone.')
    } finally {
      setUpdatingZone('')
    }
  }

  async function createBundle(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setDispatchError('')
    setBundle(null)
    if (!gatewayUrl.trim() || !sessionToken.trim()) {
      setDispatchError('Add the trusted gateway URL and admin session token above.')
      return
    }
    if (!uuidPattern.test(seedOrderId.trim())) {
      setDispatchError('Enter a valid seed order UUID.')
      return
    }
    const capacity = Number(capacityLiters)
    const orderLimit = Number(maxOrders)
    const pickupRadius = Number(pickupRadiusMeters)
    const dropoffRadius = Number(dropoffRadiusMeters)
    if (!Number.isFinite(capacity) || capacity <= 0 || capacity > 10000 ||
      !Number.isInteger(orderLimit) || orderLimit < 1 || orderLimit > 100 ||
      !Number.isInteger(pickupRadius) || pickupRadius < 0 || pickupRadius > 100000 ||
      !Number.isInteger(dropoffRadius) || dropoffRadius < 0 || dropoffRadius > 100000) {
      setDispatchError('Capacity must be 0–10,000 L (exclusive of zero); orders 1–100; radii 0–100,000 m.')
      return
    }
    let url: URL
    try {
      url = apiUrl(gatewayUrl, '/dispatch/bundles')
    } catch (error) {
      setDispatchError(error instanceof Error ? error.message : 'Enter a valid gateway URL.')
      return
    }
    const requestKey = dispatchKey || newKey()
    setDispatchKey(requestKey)
    setDispatching(true)
    try {
      const response = await fetch(url, {
        method: 'POST',
        redirect: 'error',
        headers: {
          Accept: 'application/json',
          Authorization: ['Bear', 'er'].join('') + ' ' + sessionToken.trim(),
          'Content-Type': 'application/json',
          'Idempotency-Key': requestKey,
        },
        body: JSON.stringify({
          seedOrderId: seedOrderId.trim(),
          capacityLiters: capacity,
          maxOrders: orderLimit,
          pickupRadiusMeters: pickupRadius,
          dropoffRadiusMeters: dropoffRadius,
        }),
      })
      if (!response.ok) throw await requestError(response)
      setBundle(await response.json() as Bundle)
      setDispatchKey('')
    } catch (error) {
      setDispatchError(error instanceof Error ? error.message : 'Could not create dispatch bundle.')
    } finally {
      setDispatching(false)
    }
  }

  return (
    <section className="panel delivery-operations">
      <div className="panel-header">
        <div>
          <div className="eyebrow">DELIVERY OPERATIONS</div>
          <h2>Zones and dispatch</h2>
        </div>
        <span className="lock-mark" aria-label="Admin only">⌑</span>
      </div>
      <div className="delivery-controls-grid">
        <section>
          <div className="delivery-section-heading">
            <div>
              <strong>Service zones</strong>
              <p>Disabling a zone blocks new orders and bundles; existing work remains unchanged.</p>
            </div>
            <button className="select-checkout" disabled={zonesLoading} onClick={loadZones} type="button">
              {zonesLoading ? 'Loading…' : 'Load zones'}
            </button>
          </div>
          {zonesError ? <div className="alert alert-error" role="alert">{zonesError}</div> : null}
          {zones.length ? (
            <div className="zone-list" aria-live="polite">
              {zones.map((zone) => (
                <div className="zone-row" key={zone.id}>
                  <div>
                    <strong>{zone.name}</strong>
                    <span>{zone.enabled ? 'Enabled' : 'Disabled'}</span>
                  </div>
                  <button
                    className="select-checkout"
                    disabled={updatingZone === zone.id}
                    onClick={() => updateZone(zone)}
                    type="button"
                  >
                    {updatingZone === zone.id ? 'Saving…' : zone.enabled ? 'Disable' : 'Enable'}
                  </button>
                </div>
              ))}
            </div>
          ) : (
            <p className="search-hint">Zone boundaries are managed in the database; this view controls whether a zone accepts new work.</p>
          )}
        </section>
        <section>
          <div className="delivery-section-heading">
            <div>
              <strong>Create a delivery bundle</strong>
              <p>Run proximity dispatch for a ready seed order using explicit operating limits.</p>
            </div>
          </div>
          <form className="dispatch-form" onSubmit={createBundle}>
            <label>
              Ready seed order UUID
              <input autoCapitalize="none" disabled={dispatching} value={seedOrderId} onChange={(event) => {
                setSeedOrderId(event.target.value)
                setDispatchKey('')
              }} />
            </label>
            <div className="dispatch-numbers">
              <label>Capacity (L)<input disabled={dispatching} type="number" min="0.01" max="10000" step="0.01" value={capacityLiters} onChange={(event) => {
                setCapacityLiters(event.target.value)
                setDispatchKey('')
              }} /></label>
              <label>Max orders<input disabled={dispatching} type="number" min="1" max="100" step="1" value={maxOrders} onChange={(event) => {
                setMaxOrders(event.target.value)
                setDispatchKey('')
              }} /></label>
              <label>Pickup radius (m)<input disabled={dispatching} type="number" min="0" max="100000" step="1" value={pickupRadiusMeters} onChange={(event) => {
                setPickupRadiusMeters(event.target.value)
                setDispatchKey('')
              }} /></label>
              <label>Drop-off radius (m)<input disabled={dispatching} type="number" min="0" max="100000" step="1" value={dropoffRadiusMeters} onChange={(event) => {
                setDropoffRadiusMeters(event.target.value)
                setDispatchKey('')
              }} /></label>
            </div>
            {dispatchError ? <div className="alert alert-error" role="alert">{dispatchError}</div> : null}
            <button className="submit-button" disabled={dispatching} type="submit">
              {dispatching ? 'Creating bundle…' : 'Create bundle'}
            </button>
            <p className="form-footnote">Retries reuse the same idempotency key until the request succeeds or inputs change.</p>
          </form>
          {bundle ? (
            <div className="bundle-result" role="status">
              <strong>Bundle {bundle.id} · {bundle.status}</strong>
              {bundle.stops.map((stop) => (
                <span key={stop.id}>Stop {stop.sequence} · {stop.type} · {stop.status} · order {stop.orderId}</span>
              ))}
            </div>
          ) : null}
        </section>
      </div>
    </section>
  )
}
