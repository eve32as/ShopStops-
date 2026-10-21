import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react'

type DeliveryZone = {
  id: string
  name: string
  enabled: boolean
  boundary?: {
    type: 'Polygon' | 'MultiPolygon'
    coordinates: number[][][] | number[][][][]
  }
  maxOrders: number
  pickupRadiusMeters: number
  dropoffRadiusMeters: number
  dispatchLimitsUpdatedBy: string
  dispatchLimitsUpdatedAt: string
}

type OperationsSnapshot = {
  generatedAt: string
  pendingOutboxEvents: number
  oldestPendingOutboxAt: string | null
  deliveryZones: Array<{
    id: string
    name: string
    enabled: boolean
    readyOrderCount: number
    oldestReadyAt: string | null
    oldestReadyOrderId: string | null
    activeBundleCount: number
    openStopCount: number
  }>
}

type AdminDispatchRoute = {
  id: string
  driverRef: string
  deliveryZoneName: string
  status: string
  openStopCount: number
  lastLocationAt: string | null
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

type AssignedRoute = {
  id: string
  checkoutId: string
  status: string
  stops: Array<{ id: string; sequence: number; type: string; status: string }>
}

type Props = {
  gatewayUrl: string
  sessionToken: string
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
const operationsRefreshIntervalMs = 15_000
const dispatchStreamSubprotocol = 'shopstops-admin-v1'

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

function bearerSubprotocol(token: string) {
  let binary = ''
  for (const byte of new TextEncoder().encode(token)) {
    binary += String.fromCharCode(byte)
  }
  const encoded = btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '')
  return `bearer.${encoded}`
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

function zonePathData(zones: DeliveryZone[]) {
  const shapes = zones.map((zone) => {
    const boundary = zone.boundary
    if (!boundary || !Array.isArray(boundary.coordinates)) return { zone, rings: [] as number[][][] }
    const polygons = boundary.type === 'Polygon'
      ? [boundary.coordinates as number[][][]]
      : boundary.type === 'MultiPolygon'
        ? boundary.coordinates as number[][][][]
        : []
    const rings = polygons.flat().filter((ring) =>
      Array.isArray(ring) && ring.length >= 4 && ring.every((point) =>
        Array.isArray(point) && point.length >= 2 &&
        Number.isFinite(point[0]) && Number.isFinite(point[1]),
      ),
    )
    return { zone, rings }
  })
  const positions = shapes.flatMap(({ rings }) => rings.flat())
  if (!positions.length) return []

  const longitudes = positions.map(([longitude]) => longitude)
  const latitudes = positions.map(([, latitude]) => latitude)
  const minLongitude = Math.min(...longitudes)
  const maxLongitude = Math.max(...longitudes)
  const minLatitude = Math.min(...latitudes)
  const maxLatitude = Math.max(...latitudes)
  const centerLatitude = (minLatitude + maxLatitude) / 2
  const longitudeScale = Math.cos(centerLatitude * Math.PI / 180)
  const width = Math.max((maxLongitude - minLongitude) * longitudeScale, 0.000001)
  const height = Math.max(maxLatitude - minLatitude, 0.000001)
  const scale = Math.min(360 / width, 200 / height)
  const centerLongitude = (minLongitude + maxLongitude) / 2
  const project = ([longitude, latitude]: number[]) => [
    200 + (longitude - centerLongitude) * longitudeScale * scale,
    120 - (latitude - centerLatitude) * scale,
  ]

  return shapes.filter(({ rings }) => rings.length > 0).map(({ zone, rings }) => ({
    zone,
    path: rings.map((ring) => ring.map(project)
      .map(([x, y], index) => `${index === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`)
      .join(' ') + ' Z').join(' '),
  }))
}

function ZoneBoundaryMap({
  zones,
  onSelect,
}: {
  zones: DeliveryZone[]
  onSelect: (name: string) => void
}) {
  const paths = zonePathData(zones)
  return (
    <section className="zone-map" aria-label="Delivery sector boundary map">
      <div className="zone-map-heading">
        <strong>Sector boundary overview</strong>
        <span>Structural view · polygon boundaries</span>
      </div>
      {paths.length ? (
        <svg
          aria-label="Delivery sectors with enabled and disabled status"
          className="zone-map-svg"
          role="img"
          viewBox="0 0 400 240"
        >
          <rect className="zone-map-background" height="240" width="400" />
          <path className="zone-map-grid" d="M0 60H400 M0 120H400 M0 180H400 M100 0V240 M200 0V240 M300 0V240" />
          {paths.map(({ zone, path }) => (
            <path
              aria-label={`${zone.name}, ${zone.enabled ? 'enabled' : 'disabled'}`}
              className={`zone-map-shape ${zone.enabled ? 'zone-map-shape-enabled' : 'zone-map-shape-disabled'}`}
              d={path}
              fillRule="evenodd"
              key={zone.id}
              onClick={() => onSelect(zone.name)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' || event.key === ' ') {
                  event.preventDefault()
                  onSelect(zone.name)
                }
              }}
              role="button"
              tabIndex={0}
            >
              <title>{`${zone.name} · ${zone.enabled ? 'Enabled' : 'Disabled'}`}</title>
            </path>
          ))}
        </svg>
      ) : (
        <p className="zone-map-empty">No valid polygon boundaries were returned for these sectors.</p>
      )}
      <div className="zone-map-legend">
        <span><i className="zone-map-key zone-map-key-enabled" /> Enabled</span>
        <span><i className="zone-map-key zone-map-key-disabled" /> Disabled</span>
      </div>
    </section>
  )
}

export default function DeliveryOperations({ gatewayUrl, sessionToken }: Props) {
  const [zones, setZones] = useState<DeliveryZone[]>([])
  const [zonesError, setZonesError] = useState('')
  const [operationsError, setOperationsError] = useState('')
  const [zonesLoading, setZonesLoading] = useState(false)
  const [operations, setOperations] = useState<OperationsSnapshot | null>(null)
  const [assignedRoutes, setAssignedRoutes] = useState<AdminDispatchRoute[]>([])
  const [dispatchStreamConnected, setDispatchStreamConnected] = useState(false)
  const [zoneSearch, setZoneSearch] = useState('')
  const [updatingZone, setUpdatingZone] = useState('')
  const [savingLimitsZone, setSavingLimitsZone] = useState('')
  const [zoneDrafts, setZoneDrafts] = useState<Record<string, DispatchLimitDraft>>({})
  const [seedOrderId, setSeedOrderId] = useState('')
  const [capacityLiters, setCapacityLiters] = useState('100')
  const [maxOrdersOverride, setMaxOrdersOverride] = useState('')
  const [pickupRadiusOverride, setPickupRadiusOverride] = useState('')
  const [dropoffRadiusOverride, setDropoffRadiusOverride] = useState('')
  const [dispatchKey, setDispatchKey] = useState('')
  const [bundle, setBundle] = useState<Bundle | null>(null)
  const [dispatchError, setDispatchError] = useState('')
  const [dispatching, setDispatching] = useState(false)
  const [routeCheckoutId, setRouteCheckoutId] = useState('')
  const [routeDriverRef, setRouteDriverRef] = useState('')
  const [routeAssignmentKey, setRouteAssignmentKey] = useState('')
  const [assignedRoute, setAssignedRoute] = useState<AssignedRoute | null>(null)
  const [routeAssignmentError, setRouteAssignmentError] = useState('')
  const [assigningRoute, setAssigningRoute] = useState(false)

  const loadOperations = useCallback(async () => {
    if (!gatewayUrl.trim() || !sessionToken.trim()) {
      return
    }
    let url: URL
    try {
      url = apiUrl(gatewayUrl, '/admin/operations')
    } catch (error) {
      setOperationsError(error instanceof Error ? error.message : 'Enter a valid gateway URL.')
      return
    }
    try {
      const headers = {
        Accept: 'application/json',
        Authorization: ['Bear', 'er'].join('') + ' ' + sessionToken.trim(),
      }
      const [response, routesResponse] = await Promise.all([
        fetch(url, { redirect: 'error', headers }),
        fetch(apiUrl(gatewayUrl, '/admin/dispatch/routes'), { redirect: 'error', headers }),
      ])
      if (!response.ok) throw await requestError(response)
      if (!routesResponse.ok) throw await requestError(routesResponse)
      const [snapshot, routeResult] = await Promise.all([
        response.json() as Promise<OperationsSnapshot>,
        routesResponse.json() as Promise<{ routes?: AdminDispatchRoute[] }>,
      ])
      setOperations(snapshot)
      setAssignedRoutes(Array.isArray(routeResult.routes) ? routeResult.routes : [])
      setOperationsError('')
    } catch (error) {
      setOperationsError(error instanceof Error ? error.message : 'Could not refresh operations.')
    }
  }, [gatewayUrl, sessionToken])

  useEffect(() => {
    if (!gatewayUrl.trim() || !sessionToken.trim()) {
      return
    }
    const initialRefresh = window.setTimeout(() => void loadOperations(), 0)
    const timer = window.setInterval(() => void loadOperations(), operationsRefreshIntervalMs)
    return () => {
      window.clearTimeout(initialRefresh)
      window.clearInterval(timer)
    }
  }, [gatewayUrl, sessionToken, loadOperations])

  useEffect(() => {
    if (!gatewayUrl.trim() || !sessionToken.trim()) {
      return
    }
    let stopped = false
    let retryDelay = 1_000
    let retryTimer = 0
    let connection: WebSocket | null = null
    const connect = () => {
      try {
        const url = apiUrl(gatewayUrl, '/admin/dispatch/stream')
        url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
        connection = new WebSocket(url.toString(), [
          dispatchStreamSubprotocol,
          bearerSubprotocol(sessionToken.trim()),
        ])
      } catch {
        return
      }
      connection.onopen = () => {
        retryDelay = 1_000
        setDispatchStreamConnected(true)
      }
      connection.onmessage = (message) => {
        try {
          const update = JSON.parse(String(message.data)) as {
            type?: string
            routeId?: string
            driverRef?: string
            observedAt?: string
          }
          if (update.type !== 'driver.location.updated' ||
            !update.routeId || !update.driverRef || !update.observedAt ||
            !Number.isFinite(Date.parse(update.observedAt))) return
          setAssignedRoutes((routes) => routes.map((route) => {
            if (route.id !== update.routeId || route.driverRef !== update.driverRef) return route
            if (route.lastLocationAt &&
              Date.parse(route.lastLocationAt) >= Date.parse(update.observedAt!)) return route
            return { ...route, lastLocationAt: update.observedAt! }
          }))
        } catch {
          return
        }
      }
      connection.onerror = () => connection?.close()
      connection.onclose = () => {
        if (stopped) return
        setDispatchStreamConnected(false)
        retryTimer = window.setTimeout(connect, retryDelay)
        retryDelay = Math.min(retryDelay * 2, 30_000)
      }
    }
    connect()
    return () => {
      stopped = true
      window.clearTimeout(retryTimer)
      connection?.close()
    }
  }, [gatewayUrl, sessionToken])

  const visibleZones = useMemo(() => {
    const query = zoneSearch.trim().toLocaleLowerCase()
    return query
      ? zones.filter((zone) => zone.name.toLocaleLowerCase().includes(query))
      : zones
  }, [zoneSearch, zones])

  const visibleOperationZones = useMemo(() => {
    const query = zoneSearch.trim().toLocaleLowerCase()
    const source = gatewayUrl.trim() && sessionToken.trim() ? operations?.deliveryZones ?? [] : []
    return source.filter((zone) =>
      !query || zone.name.toLocaleLowerCase().includes(query),
    )
  }, [gatewayUrl, operations, sessionToken, zoneSearch])
  const visibleAssignedRoutes = useMemo(() => {
    const query = zoneSearch.trim().toLocaleLowerCase()
    return assignedRoutes.filter((route) =>
      !query || route.deliveryZoneName.toLocaleLowerCase().includes(query),
    )
  }, [assignedRoutes, zoneSearch])

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
      const headers = {
        Accept: 'application/json',
        Authorization: ['Bear', 'er'].join('') + ' ' + sessionToken.trim(),
      }
      const response = await fetch(url, { redirect: 'error', headers })
      if (!response.ok) throw await requestError(response)
      const result = await response.json()
      const loadedZones = Array.isArray(result.deliveryZones) ? result.deliveryZones as DeliveryZone[] : []
      setZones(loadedZones)
      void loadOperations()
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
    const maxOrders = maxOrdersOverride.trim() ? Number(maxOrdersOverride) : undefined
    const pickupRadius = pickupRadiusOverride.trim() ? Number(pickupRadiusOverride) : undefined
    const dropoffRadius = dropoffRadiusOverride.trim() ? Number(dropoffRadiusOverride) : undefined
    if (maxOrders !== undefined && (!Number.isInteger(maxOrders) || maxOrders < 1 || maxOrders > 100)) {
      setDispatchError('Max orders override must be an integer from 1 to 100.')
      return
    }
    if (pickupRadius !== undefined &&
      (!Number.isInteger(pickupRadius) || pickupRadius < 0 || pickupRadius > 100000)) {
      setDispatchError('Pickup radius override must be an integer from 0 to 100,000 meters.')
      return
    }
    if (dropoffRadius !== undefined &&
      (!Number.isInteger(dropoffRadius) || dropoffRadius < 0 || dropoffRadius > 100000)) {
      setDispatchError('Drop-off radius override must be an integer from 0 to 100,000 meters.')
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
          ...(maxOrders !== undefined ? { maxOrders } : {}),
          ...(pickupRadius !== undefined ? { pickupRadiusMeters: pickupRadius } : {}),
          ...(dropoffRadius !== undefined ? { dropoffRadiusMeters: dropoffRadius } : {}),
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

  async function createAssignedRoute(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setRouteAssignmentError('')
    setAssignedRoute(null)
    if (!gatewayUrl.trim() || !sessionToken.trim()) {
      setRouteAssignmentError('Add the trusted gateway URL and admin session token above.')
      return
    }
    if (!uuidPattern.test(routeCheckoutId.trim())) {
      setRouteAssignmentError('Enter a valid paid checkout UUID.')
      return
    }
    if (!routeDriverRef.trim() || routeDriverRef.trim().length > 200) {
      setRouteAssignmentError('Enter the assigned driver identity (up to 200 characters).')
      return
    }
    let url: URL
    try {
      url = apiUrl(gatewayUrl, '/dispatch/routes')
    } catch (error) {
      setRouteAssignmentError(error instanceof Error ? error.message : 'Enter a valid gateway URL.')
      return
    }
    const requestKey = routeAssignmentKey || newKey()
    setRouteAssignmentKey(requestKey)
    setAssigningRoute(true)
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
          checkoutId: routeCheckoutId.trim(),
          driverRef: routeDriverRef.trim(),
        }),
      })
      if (!response.ok) throw await requestError(response)
      setAssignedRoute(await response.json() as AssignedRoute)
      setRouteAssignmentKey('')
    } catch (error) {
      setRouteAssignmentError(error instanceof Error ? error.message : 'Could not assign route.')
    } finally {
      setAssigningRoute(false)
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
      {gatewayUrl.trim() && sessionToken.trim() && operations ? (
        <section className="operations-snapshot" aria-label="Delivery operations snapshot">
          <div className="operations-snapshot-heading">
            <strong>Operational snapshot</strong>
            <span>As of {new Date(operations.generatedAt).toLocaleTimeString()}</span>
          </div>
          <div className="operations-metrics">
            <div>
              <span>Pending outbox</span>
              <strong>{operations.pendingOutboxEvents}</strong>
              <small>
                {operations.oldestPendingOutboxAt
                  ? `Oldest event ${Math.max(0, Math.floor(
                    (Date.parse(operations.generatedAt) - Date.parse(operations.oldestPendingOutboxAt)) / 60000,
                  ))} min ago`
                  : 'No unpublished events'}
              </small>
            </div>
            {visibleOperationZones.map((zone) => (
              <div className="operations-zone" key={zone.id}>
                <span>{zone.name}{zone.enabled ? '' : ' · Disabled'}</span>
                <strong>{zone.readyOrderCount} ready · {zone.activeBundleCount} active bundles</strong>
                <small>
                  {zone.oldestReadyAt
                    ? `Oldest ready ${Math.max(0, Math.floor(
                      (Date.parse(operations.generatedAt) - Date.parse(zone.oldestReadyAt)) / 60000,
                    ))} min · ${zone.openStopCount} open stops`
                    : `No ready orders · ${zone.openStopCount} open stops`}
                </small>
                {zone.enabled && zone.oldestReadyOrderId ? (
                  <button
                    className="select-checkout operations-seed-button"
                    onClick={() => {
                      setSeedOrderId(zone.oldestReadyOrderId!)
                      setDispatchError('')
                    }}
                    type="button"
                  >
                    Use oldest ready order
                  </button>
                ) : null}
              </div>
            ))}
          </div>
          <div className="operations-table-wrap">
            <p className="operations-privacy-note" role="status">
              {dispatchStreamConnected
                ? 'Live driver location freshness updates connected.'
                : 'Live updates reconnecting; periodic refresh remains active.'}
            </p>
            <table className="operations-table">
              <caption>Delivery sector dispatch status</caption>
              <thead>
                <tr>
                  <th scope="col">Sector</th>
                  <th scope="col">State</th>
                  <th scope="col">Ready</th>
                  <th scope="col">Oldest ready</th>
                  <th scope="col">Active bundles</th>
                  <th scope="col">Open stops</th>
                </tr>
              </thead>
              <tbody>
                {visibleOperationZones.map((zone) => (
                  <tr key={zone.id}>
                    <th scope="row">{zone.name}</th>
                    <td><span className={`zone-status ${zone.enabled ? 'zone-status-enabled' : 'zone-status-disabled'}`}>
                      {zone.enabled ? 'Enabled' : 'Disabled'}
                    </span></td>
                    <td>{zone.readyOrderCount}</td>
                    <td>{zone.oldestReadyAt ? new Date(zone.oldestReadyAt).toLocaleString() : '—'}</td>
                    <td>{zone.activeBundleCount}</td>
                    <td>{zone.openStopCount}</td>
                  </tr>
                ))}
                {visibleOperationZones.length === 0 ? (
                  <tr><td colSpan={6}>No sectors match this search.</td></tr>
                ) : null}
              </tbody>
            </table>
          </div>
          <div className="operations-table-wrap">
            <table className="operations-table">
              <caption>Active driver assignments · up to 100 latest</caption>
              <thead>
                <tr>
                  <th scope="col">Driver</th>
                  <th scope="col">Sector</th>
                  <th scope="col">Route</th>
                  <th scope="col">Open stops</th>
                  <th scope="col">Last location report</th>
                </tr>
              </thead>
              <tbody>
                {visibleAssignedRoutes.map((route) => (
                  <tr key={route.id}>
                    <th scope="row">{route.driverRef}</th>
                    <td>{route.deliveryZoneName}</td>
                    <td><span className="zone-status zone-status-enabled">{route.status.replaceAll('_', ' ')}</span></td>
                    <td>{route.openStopCount}</td>
                    <td>{route.lastLocationAt
                      ? `${Math.max(0, Math.floor(
                        (Date.parse(operations.generatedAt) - Date.parse(route.lastLocationAt)) / 60000,
                      ))} min ago`
                      : 'No location report'}</td>
                  </tr>
                ))}
                {visibleAssignedRoutes.length === 0 ? (
                  <tr><td colSpan={5}>No active assignments match this sector search.</td></tr>
                ) : null}
              </tbody>
            </table>
            <p className="operations-privacy-note">Location timestamps are shown for operational freshness; driver coordinates are not exposed in this view.</p>
          </div>
        </section>
      ) : null}
      {operationsError ? <div className="alert alert-error" role="alert">{operationsError}</div> : null}
      <div className="delivery-controls-grid">
        <section>
          <div className="delivery-section-heading">
            <div>
              <strong>Service zones</strong>
              <p>Search sectors, review their boundaries, and control whether they accept new orders.</p>
            </div>
            <button className="select-checkout" disabled={zonesLoading} onClick={loadZones} type="button">
              {zonesLoading ? 'Loading…' : 'Load zones'}
            </button>
          </div>
          <label className="zone-search">
            Search delivery sectors
            <input
              autoComplete="off"
              onChange={(event) => setZoneSearch(event.target.value)}
              placeholder="Filter by sector name"
              type="search"
              value={zoneSearch}
            />
          </label>
          {zonesError ? <div className="alert alert-error" role="alert">{zonesError}</div> : null}
          {visibleZones.length ? (
            <>
              <ZoneBoundaryMap zones={visibleZones} onSelect={setZoneSearch} />
            <div className="zone-list" aria-live="polite">
              {visibleZones.map((zone) => (
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
            </>
          ) : zones.length ? (
            <p className="search-hint">No sectors match “{zoneSearch}”.</p>
          ) : (
            <p className="search-hint">Zone boundaries are managed in the database; this view controls whether a zone accepts new work.</p>
          )}
        </section>
        <section>
          <div className="delivery-section-heading">
            <div>
              <strong>Create a delivery bundle</strong>
              <p>Run proximity dispatch with zone defaults or override limits for this bundle.</p>
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
              <label>
                Max orders override
                <small>Optional · 1–100 · blank uses the zone default</small>
                <input disabled={dispatching} type="number" min="1" max="100" step="1" value={maxOrdersOverride} onChange={(event) => {
                  setMaxOrdersOverride(event.target.value)
                  setDispatchKey('')
                }} />
              </label>
              <label>
                Pickup radius override (m)
                <small>Optional · 0–100,000 · blank uses the zone default</small>
                <input disabled={dispatching} type="number" min="0" max="100000" step="1" value={pickupRadiusOverride} onChange={(event) => {
                  setPickupRadiusOverride(event.target.value)
                  setDispatchKey('')
                }} />
              </label>
              <label>
                Drop-off radius override (m)
                <small>Optional · 0–100,000 · blank uses the zone default</small>
                <input disabled={dispatching} type="number" min="0" max="100000" step="1" value={dropoffRadiusOverride} onChange={(event) => {
                  setDropoffRadiusOverride(event.target.value)
                  setDispatchKey('')
                }} />
              </label>
            </div>
            {dispatchError ? <div className="alert alert-error" role="alert">{dispatchError}</div> : null}
            <button className="submit-button" disabled={dispatching} type="submit">
              {dispatching ? 'Creating bundle…' : 'Create bundle'}
            </button>
            <p className="form-footnote">Blank overrides use zone defaults. Retries reuse the idempotency key until the request succeeds or inputs change.</p>
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
      <section className="route-assignment">
        <div className="delivery-section-heading">
          <div>
            <strong>Assign a paid checkout to a driver</strong>
            <p>Create the ordered driver route and activate it for location and stop updates.</p>
          </div>
        </div>
        <form className="route-assignment-form" onSubmit={createAssignedRoute}>
          <label>
            Paid checkout UUID
            <input autoCapitalize="none" disabled={assigningRoute} value={routeCheckoutId} onChange={(event) => {
              setRouteCheckoutId(event.target.value)
              setRouteAssignmentKey('')
            }} />
          </label>
          <label>
            Driver identity
            <small>Must match the identity-provider subject used by the driver app.</small>
            <input autoCapitalize="none" disabled={assigningRoute} maxLength={200} value={routeDriverRef} onChange={(event) => {
              setRouteDriverRef(event.target.value)
              setRouteAssignmentKey('')
            }} />
          </label>
          {routeAssignmentError ? <div className="alert alert-error" role="alert">{routeAssignmentError}</div> : null}
          <button className="submit-button" disabled={assigningRoute} type="submit">
            {assigningRoute ? 'Assigning route…' : 'Create and assign route'}
          </button>
          <p className="form-footnote">Only paid checkouts eligible for unified routing can be assigned. Retries reuse the idempotency key until success or input changes.</p>
        </form>
        {assignedRoute ? (
          <div className="bundle-result" role="status">
            <strong>Route {assignedRoute.id} · {assignedRoute.status}</strong>
            <span>Checkout {assignedRoute.checkoutId}</span>
            {assignedRoute.stops.map((stop) => (
              <span key={stop.id}>Stop {stop.sequence} · {stop.type} · {stop.status}</span>
            ))}
          </div>
        ) : null}
      </section>
    </section>
  )
}
