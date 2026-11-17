import { useState, type FormEvent } from 'react'
import { useRequestSession } from './useRequestSession'
import { dollars } from './financeInputs'
import { shopperAmount, shopperRef, shopperRequest, shopperUUID } from './shopperInputs'
import { recordState } from './recordState'

type Props = { gatewayUrl: string; sessionToken: string }
type Task = {
  id: string; orderId: string; shopperActor: string; assignedBy: string
  completionAmountCents: number; status: 'assigned' | 'verified_completed'
  eligibilityStatus: 'pending_review'; jurisdictionRef: string; paymentStatusAtAssignment: string
  completion: { verifiedBy: string; paymentStatusAtCompletion: string } | null
  earnings: { kind: 'earned_per_order'; status: 'pending_review'; completionAmountCents: number } | null
}

export default function ShopperOrders(props: Props) {
  // A credential change discards prior identity's records, forms and retry keys.
  return <ShopperOrdersPanel {...props} />
}
function ShopperOrdersPanel({ gatewayUrl, sessionToken }: Props) {
  const { session } = useRequestSession(gatewayUrl, sessionToken)
  const [orderId, setOrderId] = useState('')
  const [shopper, setShopper] = useState('')
  const [amount, setAmount] = useState('')
  const [jurisdiction, setJurisdiction] = useState('')
  const [assignmentKey, setAssignmentKey] = useState('')
  const [taskId, setTaskId] = useState('')
  const [receipt, setReceipt] = useState('')
  const [handoff, setHandoff] = useState('')
  const [items, setItems] = useState(false)
  const [substitutions, setSubstitutions] = useState(false)
  const [completionKey, setCompletionKey] = useState('')
  const [tasks, setTasks] = useState<Task[]>([])
  const [offset, setOffset] = useState(0)
  const [saved, setSaved] = useState<Task | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [stage, setStage] = useState<'assign' | 'verify' | 'review'>('assign')
  const [loaded, setLoaded] = useState(false)
  const records = recordState(busy, error, loaded, tasks.length)
  async function action(work: (signal: AbortSignal) => Promise<void>) {
    const scope = session.begin()
    if (!scope) return
    setBusy(true); setError(''); setSaved(null)
    try { await work(scope.signal) } catch (err) {
      if (!scope.signal.aborted) setError(err instanceof Error ? err.message : 'Request failed.')
    } finally { session.finish(scope); if (!scope.signal.aborted) setBusy(false) }
  }
  async function assign(event: FormEvent) {
    event.preventDefault()
    await action(async signal => {
      if (!shopperUUID.test(orderId)) throw new Error('Enter the existing merchant order UUID, not a checkout ID.')
      const body = { orderId, shopperActor: shopperRef(shopper), completionAmountCents: shopperAmount(amount), jurisdictionRef: shopperRef(jurisdiction) }
      const key = assignmentKey || crypto.randomUUID()
      setAssignmentKey(key)
      const task = await shopperRequest(gatewayUrl, sessionToken, 'POST', body, key, undefined, 0, signal, import.meta.env.DEV) as Task
      setSaved(task); setTaskId(task.id); setAssignmentKey(''); setTasks([]); setLoaded(false); setStage('verify')
    })
  }
  async function complete(event: FormEvent) {
    event.preventDefault()
    await action(async signal => {
      if (!shopperUUID.test(taskId) || !items || !substitutions) throw new Error('Confirm verified shopping and substitutions (including no substitutions) for a task UUID.')
      const body = { itemsConfirmed: items, substitutionsConfirmed: substitutions, receiptRef: shopperRef(receipt), handoffRef: shopperRef(handoff) }
      const key = completionKey || crypto.randomUUID()
      setCompletionKey(key)
      setSaved(await shopperRequest(gatewayUrl, sessionToken, 'POST', body, key, taskId, 0, signal, import.meta.env.DEV) as Task)
      setCompletionKey(''); setTasks([]); setLoaded(false); setStage('review')
    })
  }
  async function list(page: number) {
    await action(async signal => {
      const result = await shopperRequest(gatewayUrl, sessionToken, 'GET', undefined, undefined, undefined, page, signal, import.meta.env.DEV) as { tasks: Task[] }
      if (!Array.isArray(result.tasks)) throw new Error('Invalid task list response.')
      setTasks(result.tasks); setOffset(page); setLoaded(true)
    })
  }

  return <section className="panel finance-panel" id="shopper-orders" aria-labelledby="shopper-heading">
    <div className="eyebrow">SUPERVISED COMPLETION RECORDS · ADMIN ONLY</div>
    <h2 id="shopper-heading">Shopper orders</h2>
    <nav className="stage-nav" aria-label="Supervised shopper workflow">
      {(['assign', 'verify', 'review'] as const).map((item, index) => <button key={item} type="button" aria-pressed={stage === item} onClick={() => setStage(item)}>{index + 1}. {item === 'assign' ? 'Assign order' : item === 'verify' ? 'Verify handoff' : 'Review records'}</button>)}
    </nav>
    <p className="notice">Per verified completed shopping order only. Login, signup, acceptance and assignment create no earnings. No live payments or transfers. All completion earnings remain pending_review, not payable or paid.</p>
    <details className="form-section"><summary>Before work: eligibility, evidence & compliance</summary>
    <p>Eligibility remains pending review: this is not legal clearance, teen enrollment, or authorization to work. Verify identity, age, jurisdiction, supervision and employment requirements separately before actual work. Legally required compensable time, minimum wages, overtime and reimbursements need a separate compliance/payroll process; customer payment failure does not forfeit compensation for work.</p>
    <p>Uses the trusted gateway and in-memory admin token above. Store only opaque shopper and evidence IDs, never names, receipt contents, addresses, bank details, or public evidence URLs. The authenticated admin is the verifier.</p>
    </details>
    <div hidden={stage !== 'assign'}>
    <p>Start with an existing merchant order and an explicitly approved completion amount. Assignment creates a scheduled snapshot only, not earnings.</p>
    <form onSubmit={assign}><fieldset disabled={busy}>
      <legend>Record supervised task assignment — no earnings yet</legend>
      <div className="finance-inputs">
        <label>Existing merchant order UUID<input required value={orderId} onChange={e => { setOrderId(e.target.value); setAssignmentKey('') }} /></label>
        <label>Opaque shopper actor<input required maxLength={100} autoComplete="off" value={shopper} onChange={e => { setShopper(e.target.value); setAssignmentKey('') }} /></label>
        <label>Explicitly approved completion amount (USD)<input required inputMode="decimal" placeholder="No default rate" value={amount} onChange={e => { setAmount(e.target.value); setAssignmentKey('') }} /></label>
        <label>Jurisdiction reference — not clearance<input required maxLength={100} value={jurisdiction} onChange={e => { setJurisdiction(e.target.value); setAssignmentKey('') }} /></label>
      </div>
      <button className="submit-button" type="submit">Record assignment</button>
      {assignmentKey && <p>Assignment retry key: {assignmentKey}</p>}
    </fieldset></form>
    </div>
    <div hidden={stage !== 'verify'}>
    <p>Select an assigned task from Review records, or enter its UUID. Verify private evidence and the actual handoff before recording completion.</p>
    <form onSubmit={complete}><fieldset disabled={busy}>
      <legend>Verify completed shopping and handoff</legend>
      <div className="finance-inputs">
        <label>Task UUID<input required value={taskId} onChange={e => { setTaskId(e.target.value); setCompletionKey('') }} /></label>
        <label>Private receipt evidence reference<input required maxLength={100} value={receipt} onChange={e => { setReceipt(e.target.value); setCompletionKey('') }} /></label>
        <label>Private handoff proof reference<input required maxLength={100} value={handoff} onChange={e => { setHandoff(e.target.value); setCompletionKey('') }} /></label>
      </div>
      <label><input type="checkbox" required checked={items} onChange={e => { setItems(e.target.checked); setCompletionKey('') }} /> I verified the order's item shopping and receipt evidence.</label>
      <label><input type="checkbox" required checked={substitutions} onChange={e => { setSubstitutions(e.target.checked); setCompletionKey('') }} /> I verified authorized substitutions, or confirmed none were needed, and the handoff proof.</label>
      <button className="submit-button" type="submit">Record verified completion — pending earnings review</button>
      {completionKey && <p>Completion retry key: {completionKey}</p>}
    </fieldset></form>
    </div>
    <p>Identical retries retain their UUID key after failures. Do not edit a timed-out request or generate a new key to bypass a conflict. Each order has one immutable assignment and one completion; reassignment is intentionally unavailable.</p>
    {error && <div className="alert alert-error" role="alert">{error}</div>}
    {saved && <div className="alert alert-success" role="status">Task {saved.id}: {saved.status}. {saved.earnings ? `${dollars(saved.earnings.completionAmountCents)} earned per order — pending_review; no money transferred.` : 'Scheduled compensation snapshot only; no earnings created.'}</div>}
    {busy && <p role="status">Contacting gateway…</p>}
    <div hidden={stage !== 'review'}>
    <h3>Recorded tasks — 50 per page</h3>
    {records.kind !== 'ready' && <div className="empty-state" role={records.kind === 'error' ? 'alert' : 'status'}><h3>{records.title}</h3><p>{records.detail}</p></div>}
    <div className="shopper-page-actions">
      <button disabled={busy} onClick={() => list(offset)}>Refresh tasks</button>
      <button disabled={busy || offset === 0} onClick={() => list(Math.max(0, offset - 50))}>Previous</button>
      <button disabled={busy || tasks.length < 50 || offset >= 100000} onClick={() => list(offset + 50)}>Next</button>
      <span>Offset {offset}</span>
    </div>
    <div className="shopper-task-list">{tasks.map(task => <article className="notice" key={task.id}>
      <div><strong>{task.status} · {dollars(task.completionAmountCents)} {task.earnings ? 'earned, pending review' : 'scheduled, not earned'}</strong>
        <p>Order {task.orderId} · Shopper {task.shopperActor} · Task {task.id}</p>
        <p>Eligibility: {task.eligibilityStatus} · Jurisdiction: {task.jurisdictionRef} · Payment at assignment: {task.paymentStatusAtAssignment}</p>
        {task.completion && <p>Verified by {task.completion.verifiedBy} · Payment at completion: {task.completion.paymentStatusAtCompletion}. No transfer.</p>}
        {task.status === 'assigned' && <button disabled={busy} onClick={() => { setTaskId(task.id); setCompletionKey(''); setReceipt(''); setHandoff(''); setItems(false); setSubstitutions(false); setStage('verify') }}>Select for verification</button>}
      </div>
    </article>)}</div>
    </div>
  </section>
}