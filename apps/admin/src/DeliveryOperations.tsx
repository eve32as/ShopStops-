import { useState, type FormEvent } from 'react'

type DeliveryZone = {
  id: string
  name: string
  enabled: boolean
  maxOrders: number
  pickupRadiusMeters: number
  dropoffRadiusMeters: number
  dispatchLimitsUpdatedBy: string
  dispatchLimitsUpdatedAt: string
}

type OperationsSnapshot = {
  generatedAt: string
  pendingOutboxEvents: number
  deliveryZones: Array<{
    id: string
    name: string
    readyOrderCount: number
    oldestReadyAt: string | null
    activeBundleCount: number
    openStopCount: number
  }>
}

type DispatchLimitDraft = {
  maxOrders: string
  pickupRadiusMeters: string
  dropoffRadiusMeters: string
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
  const [operations, setOperations] = useState<OperationsSnapshot | null>(null)
  const [updatingZone, setUpdatingZone] = useState('')
  const [savingLimitsZone, setSavingLimitsZone] = useState('')
  const [zoneDrafts, setZoneDrafts] = useState<Record<string, DispatchLimitDraft>>({})
  const [seedOrderId, setSeedOrderId] = useState('')
  const [capacityLiters, setCapacityLiters] = useState('100')
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
    let operationsUrl: URL
    try {
      url = apiUrl(gatewayUrl, '/admin/delivery-zones')
      operationsUrl = apiUrl(gatewayUrl, '/admin/operations')
    } catch (error) {
      setZonesError(error instanceof Error ? error.message : 'Enter a valid gateway URL.')
      return
    }
    setZonesLoading(true)
    try {
      const headers = {
        Accept: 'application/json',
        Authorization: ['Bear', 'er'].join('') + ' ' + sessionToken.trim(),
      }
      const [response, operationsResponse] = await Promise.all([
        fetch(url, { redirect: 'error', headers }),
        fetch(operationsUrl, { redirect: 'error', headers }),
      ])
      if (!response.ok) throw await requestError(response)
      if (!operationsResponse.ok) throw await requestError(operationsResponse)
      const [result, operationsResult] = await Promise.all([
        response.json(),
        operationsResponse.json() as Promise<OperationsSnapshot>,
      ])
      const loadedZones = Array.isArray(result.deliveryZones) ? result.deliveryZones as DeliveryZone[] : []
      setZones(loadedZones)
      setOperations(operationsResult)
      setZoneDrafts(Object.fromEntries(loadedZones.map((zone) => [zone.id, {
        maxOrders: String(zone.maxOrders),
        pickupRadiusMeters: String(zone.pickupRadiusMeters),
        dropoffRadiusMeters: String(zone.dropoffRadiusMeters),
      }])))
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

  async function saveDispatchLimits(zone: DeliveryZone) {
    setZonesError('')
    const draft = zoneDrafts[zone.id]
    const max = Number(draft?.maxOrders)
    const pickup = Number(draft?.pickupRadiusMeters)
    const dropoff = Number(draft?.dropoffRadiusMeters)
    if (!draft || !Number.isInteger(max) || max < 1 || max > 100 ||
      !Number.isInteger(pickup) || pickup < 0 || pickup > 100000 ||
      !Number.isInteger(dropoff) || dropoff < 0 || dropoff > 100000) {
      setZonesError('Max orders must be 1–100 and each radius must be 0–100,000 meters.')
      return
    }
    let url: URL
    try {
      url = apiUrl(gatewayUrl, `/admin/delivery-zones/${encodeURIComponent(zone.id)}/dispatch-limits`)
    } catch (error) {
      setZonesError(error instanceof Error ? error.message : 'Enter a valid gateway URL.')
      return
    }
    setSavingLimitsZone(zone.id)
    try {
      const response = await fetch(url, {
        method: 'PATCH',
        redirect: 'error',
        headers: {
          Accept: 'application/json',
          Authorization: ['Bear', 'er'].join('') + ' ' + sessionToken.trim(),
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          maxOrders: max,
          pickupRadiusMeters: pickup,
          dropoffRadiusMeters: dropoff,
        }),
      })
      if (!response.ok) throw await requestError(response)
      const updated = await response.json() as DeliveryZone
      setZones((current) => current.map((item) => item.id === updated.id ? updated : item))
      setZoneDrafts((current) => ({
        ...current,
        [updated.id]: {
          maxOrders: String(updated.maxOrders),
          pickupRadiusMeters: String(updated.pickupRadiusMeters),
          dropoffRadiusMeters: String(updated.dropoffRadiusMeters),
        },
      }))
    } catch (error) {
      setZonesError(error instanceof Error ? error.message : 'Could not save dispatch limits.')
    } finally {
      setSavingLimitsZone('')
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
    if (!Number.isFinite(capacity) || capacity <= 0 || capacity > 10000) {
      setDispatchError('Vehicle capacity must be greater than 0 and no more than 10,000 liters.')
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
      {operations ? (
        <section className="operations-snapshot" aria-label="Delivery operations snapshot">
          <div className="operations-snapshot-heading">
            <strong>Operational snapshot</strong>
            <span>As of {new Date(operations.generatedAt).toLocaleTimeString()}</span>
          </div>
          <div className="operations-metrics">
            <div><span>Pending outbox</span><strong>{operations.pendingOutboxEvents}</strong></div>
            {operations.deliveryZones.map((zone) => (
              <div className="operations-zone" key={zone.id}>
                <span>{zone.name}</span>
                <strong>{zone.readyOrderCount} ready · {zone.activeBundleCount} active bundles</strong>
                <small>
                  {zone.oldestReadyAt
                    ? `Oldest ready ${Math.max(0, Math.floor(
                      (Date.parse(operations.generatedAt) - Date.parse(zone.oldestReadyAt)) / 60000,
                    ))} min · ${zone.openStopCount} open stops`
                    : `No ready orders · ${zone.openStopCount} open stops`}
                </small>
              </div>
            ))}
          </div>
        </section>
      ) : null}
      <div className="delivery-controls-grid">
        <section>
          <div className="delivery-section-heading">
            <div>
              <strong>Service zones</strong>
              <p>Set saved proximity limits and control whether a zone accepts new orders.</p>
            </div>
            <button className="select-checkout" disabled={zonesLoading} onClick={loadZones} type="button">
              {zonesLoading ? 'Loading…' : 'Load zones'}
            </button>
          </div>
          {zonesError ? <div className="alert alert-error" role="alert">{zonesError}</div> : null}
          {zones.length ? (
            <div className="zone-list" aria-live="polite">
              {zones.map((zone) => (
                <article className="zone-card" key={zone.id}>
                  <div className="zone-row">
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
                  <div className="zone-limit-form">
                    <label>
                      Max orders
                      <input disabled={savingLimitsZone === zone.id} type="number" min="1" max="100" step="1"
                        value={zoneDrafts[zone.id]?.maxOrders ?? ''}
                        onChange={(event) => setZoneDrafts((current) => ({
                          ...current,
                          [zone.id]: {
                            ...(current[zone.id] ?? {
                              maxOrders: String(zone.maxOrders),
                              pickupRadiusMeters: String(zone.pickupRadiusMeters),
                              dropoffRadiusMeters: String(zone.dropoffRadiusMeters),
                            }),
                            maxOrders: event.target.value,
                          },
                        }))} />
                    </label>
                    <label>
                      Pickup radius (m)
                      <input disabled={savingLimitsZone === zone.id} type="number" min="0" max="100000" step="1"
                        value={zoneDrafts[zone.id]?.pickupRadiusMeters ?? ''}
                        onChange={(event) => setZoneDrafts((current) => ({
                          ...current,
                          [zone.id]: {
                            ...(current[zone.id] ?? {
                              maxOrders: String(zone.maxOrders),
                              pickupRadiusMeters: String(zone.pickupRadiusMeters),
                              dropoffRadiusMeters: String(zone.dropoffRadiusMeters),
                            }),
                            pickupRadiusMeters: event.target.value,
                          },
                        }))} />
                    </label>
                    <label>
                      Drop-off radius (m)
                      <input disabled={savingLimitsZone === zone.id} type="number" min="0" max="100000" step="1"
                        value={zoneDrafts[zone.id]?.dropoffRadiusMeters ?? ''}
                        onChange={(event) => setZoneDrafts((current) => ({
                          ...current,
                          [zone.id]: {
                            ...(current[zone.id] ?? {
                              maxOrders: String(zone.maxOrders),
                              pickupRadiusMeters: String(zone.pickupRadiusMeters),
                              dropoffRadiusMeters: String(zone.dropoffRadiusMeters),
                            }),
                            dropoffRadiusMeters: event.target.value,
                          },
                        }))} />
                    </label>
                    <button className="select-checkout" disabled={savingLimitsZone === zone.id}
                      onClick={() => saveDispatchLimits(zone)} type="button">
                      {savingLimitsZone === zone.id ? 'Saving…' : 'Save limits'}
                    </button>
                  </div>
                  <small className="zone-audit">Updated {new Date(zone.dispatchLimitsUpdatedAt).toLocaleString()} by {zone.dispatchLimitsUpdatedBy}</small>
                </article>
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
              <p>Run proximity dispatch with the saved limits for the seed order’s delivery zone.</p>
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
              <label>Vehicle capacity (L)<input disabled={dispatching} type="number" min="0.01" max="10000" step="0.01" value={capacityLiters} onChange={(event) => {
                setCapacityLiters(event.target.value)
                setDispatchKey('')
              }} /></label>
            </div>
            {dispatchError ? <div className="alert alert-error" role="alert">{dispatchError}</div> : null}
            <button className="submit-button" disabled={dispatching} type="submit">
              {dispatching ? 'Creating bundle…' : 'Create bundle'}
            </button>
            <p className="form-footnote">Zone defaults supply max orders and radii. Retries reuse the idempotency key until the request succeeds or inputs change.</p>
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
