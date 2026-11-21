import { useCallback, useState } from 'react'
import { gatewayURL, requestJSON } from './transport.ts'

type Overview = {
  kind: string
  generatedAt: string
  checkouts: Record<string, string | number>
  refunds: { pending: number; succeeded: number; failed: number; canceled: number }
  manualCosts: { recordCount: number; totalCents: string }
  shopperTasks: { assigned: number; verifiedCompleted: number; pendingReviewEarnings: number }
}

export default function AdminOverview({ gatewayUrl, sessionToken }: { gatewayUrl: string; sessionToken: string }) {
  const [overview, setOverview] = useState<Overview | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true); setError('')
    try {
      const url = gatewayURL(gatewayUrl, import.meta.env.DEV)
      url.pathname = `${url.pathname.replace(/\/+$/, '')}/admin/overview`
      if (!sessionToken.trim()) throw new Error('Add the admin session token to load live aggregate counts.')
      setOverview(await requestJSON<Overview>(url, sessionToken, { method: 'GET', redirect: 'error' }))
    } catch (err) {
      setOverview(null)
      setError(err instanceof Error ? err.message : 'Overview could not be loaded.')
    } finally { setLoading(false) }
  }, [gatewayUrl, sessionToken])

  return <>
    <div className="notice">{sessionToken.trim() ? 'Live authorized aggregate counts. These are record counts, not verified revenue or payable worker totals.' : 'No live operational KPIs are loaded. Enter an admin session token to request aggregate counts.'}</div>
    <button type="button" className="submit-button" disabled={loading} onClick={() => void load()}>{loading ? 'Loading overview…' : overview ? 'Refresh overview' : 'Load overview'}</button>
    {error ? <div className="alert alert-error" role="alert">{error}</div> : null}
    {overview && <div className="overview-grid" aria-live="polite">
      <article className="overview-card"><div className="eyebrow">CHECKOUT STATES</div><h3>{Object.values(overview.checkouts).reduce<number>((sum, value) => sum + Number(value || 0), 0)} records</h3><p>{Object.entries(overview.checkouts).map(([state, count]) => `${state}: ${count}`).join(' · ') || 'No checkouts returned'}</p></article>
      <article className="overview-card"><div className="eyebrow">REFUNDS</div><h3>{overview.refunds.pending} pending</h3><p>{overview.refunds.succeeded} succeeded · {overview.refunds.failed} failed · {overview.refunds.canceled} canceled</p></article>
      <article className="overview-card"><div className="eyebrow">SHOPPER REVIEW</div><h3>{overview.shopperTasks.assigned} assigned</h3><p>{overview.shopperTasks.verifiedCompleted} completed · {overview.shopperTasks.pendingReviewEarnings} pending-review earnings</p></article>
    </div>}
  </>
}
