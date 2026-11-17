import { useState, type FormEvent } from 'react'
import { dollars, financeURL, parseScenarioInput, parseMoney } from './financeInputs'
import { requestJSON } from './transport.ts'
import { useRequestSession } from './useRequestSession'

const fields = [
  ['checkouts', 'Monthly checkouts (one checkout per drop)', '1000', false],
  ['deliveryFeeCents', 'Delivery fee per checkout — assumed $', '6.00', true],
  ['serviceFeeCents', 'Platform/service fee per checkout — assumed $', '2.00', true],
  ['merchandiseSubtotalCents', 'Merchandise subtotal per checkout (exclude tax, tips and all fees) $', '0.00', true],
  ['additionalPickupsPerCheckout', 'Platform extra pickups per checkout (distinct locations minus first, 0–100)', '0', false],
  ['assumedPaidMerchants', 'Assumed paid merchants (not verified)', '0', false],
  ['subscriptionCents', 'Monthly subscription per merchant — assumed $', '199.00', true],
  ['driverBaseCents', 'Driver guarantee base per bundle (excludes pickup pay) — assumed $', '5.00', true],
  ['originalPickupCents', 'Original pickup pay per bundle — assumed $', '0.00', true],
  ['additionalPickupsPerBundle', 'Driver additional pickups per bundle (separate scope, 0–100)', '0', false],
  ['pickupRadiusMilliMiles', 'Planning radius from FIRST pickup (miles, 0–0.500)', '0.500', false],
  ['shopperCentsPerCheckout', 'Separate shopper cost per checkout — assumed $', '0.00', true],
  ['fastServiceAdoptionBasisPoints', 'Optional priority service uptake forecast (0–10000 basis points)', '0', false],
  ['fastShoppingFeeCents', 'Opted-in fast shopping customer fee — assumed $', '0.00', true],
  ['fastDeliveryFeeCents', 'Opted-in fast delivery customer fee — assumed $', '0.00', true],
  ['fastShopperPremiumCents', 'Shopper premium per opted-in checkout — assumed $', '0.00', true],
  ['fastDriverPremiumCents', 'Driver premium per opted-in checkout — assumed $', '0.00', true],
  ['bundleMilliMiles', 'Bundle distance (thousandths of a mile)', '0', false],
  ['mileageCentsPerMile', 'Driver mileage per mile — assumed $', '0.00', true],
  ['bundleSeconds', 'Bundle time (seconds, include waiting)', '0', false],
  ['timeCentsPerHour', 'Driver time per hour — assumed $', '0.00', true],
  ['dropsPerBundle', 'Checkout drops per bundle (1–100, not merchants)', '1', false],
  ['processingExcessCents', 'Platform processing excess per checkout $', '0.00', true],
  ['refundLossCents', 'Unrecovered refund loss per checkout $', '0.00', true],
  ['manualVariableCents', 'Other non-overlapping variable cost per checkout $', '0.00', true],
  ['hostingCents', 'Monthly hosting — illustrative, editable $', '600.00', true],
  ['maintenanceCents', 'Monthly maintenance/support (exclude other overhead) $', '0.00', true],
  ['overheadCents', 'Monthly other overhead $', '0.00', true],
  ['payrollCents', 'Monthly staff payroll overhead (not drivers again) $', '0.00', true],
] as const

const scenarioSections = [
  { title: '1. Demand & customer fees', hint: 'Checkout volume, merchandise, platform fees and subscription assumptions.', indexes: [0, 1, 2, 3, 4, 5, 6] },
  { title: '2. Driver & shopper costs', hint: 'Separate bundle costs from per-checkout shopping costs. These are not approved pay rates.', indexes: [7, 8, 9, 10, 11, 17, 18, 19, 20, 21] },
  { title: '3. Optional priority forecast', hint: 'Leave at zero unless you are modeling an explicit scenario. This does not enable checkout features.', indexes: [12, 13, 14, 15, 16] },
  { title: '4. Platform costs & overhead', hint: 'Include each cost once. Ledger observations are not automatically imported.', indexes: [22, 23, 24, 25, 26, 27, 28] },
]

type ScenarioResult = {
  additionalPickupPlatformFeeBasisPoints: number
  additionalPickupPlatformFeePerCheckoutCents: number
  additionalPickupPlatformFeeMonthCents: number
  kind: 'estimate'
  customerFeesPerCheckoutCents: number
  customerFeesMonthCents: number
  platformServiceFeesMonthCents: number
  deliveryFeesMonthCents: number
  subscriptionForecastCents: number
  driverBundleCents: number
  driverPerCheckoutCents: number
  driverMonthCents: number
  additionalPickupCents: number
  pickupAddonBundleCents: number
  pickupBundleCents: number
  shopperMonthCents: number
  fastFeesMonthCents: number
  fastShopperPremiumMonthCents: number
  fastDriverPremiumMonthCents: number
  contributionPerCheckoutCents: number
  contributionNumeratorCents: number
  contributionDenominator: number
  contributionMonthCents: number
  fixedMonthCents: number
  maintenanceMonthCents: number
  platformRevenueForecastCents: number
  netOperatingCents: number
  netMarginBasisPoints: number | null
  breakEvenCheckouts: number | null
}
type Cost = { id: string; period: string; category: string; amountCents: number; actor: string; createdAt: string }
type CostReport = { totalCents: string; costs: Cost[]; period: string; offset: number; limit: number }

export default function FinancePlanning({ gatewayUrl, sessionToken }: { gatewayUrl: string; sessionToken: string }) {
  const { session } = useRequestSession(gatewayUrl, sessionToken)
  const [inputs, setInputs] = useState<Record<string, string>>(() => Object.fromEntries(fields.map(([key, , value]) => [key, value])))
  const [result, setResult] = useState<ScenarioResult | null>(null)
  const [period, setPeriod] = useState(() => new Date().toISOString().slice(0, 7))
  const [category, setCategory] = useState('hosting')
  const [amount, setAmount] = useState('0.00')
  const [costKey, setCostKey] = useState('')
  const [report, setReport] = useState<CostReport | null>(null)
  const [saved, setSaved] = useState<Cost | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function request(path: string, method = 'GET', body?: unknown, key?: string) {
    if (!sessionToken.trim()) throw new Error('Add your admin session token in the gateway settings above.')
    return requestJSON(financeURL(gatewayUrl, path, import.meta.env.DEV), sessionToken, {
      signal: session.scope.signal,
      method, redirect: 'error', credentials: 'omit',
      headers: { Accept: 'application/json', Authorization: `Bearer ${sessionToken.trim()}`,
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
        ...(key ? { 'Idempotency-Key': key } : {}) },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    })
  }
  async function action(work: () => Promise<void>) {
    const scope = session.begin()
    if (!scope) return
    setBusy(true); setError('')
    try { await work() } catch (err) { if (!scope.signal.aborted) setError(err instanceof Error ? err.message : 'Finance request failed.') }
    finally { session.finish(scope); if (!scope.signal.aborted) setBusy(false) }
  }
  function validPeriod() {
    if (!/^(20\d{2}|2100)-(0[1-9]|1[0-2])$/.test(period)) throw new Error('Use a UTC accounting month YYYY-MM, 2000..2100.')
  }
  async function calculate(event: FormEvent) {
    event.preventDefault(); setResult(null)
    await action(async () => {
      const scenario = Object.fromEntries(fields.map(([key, label, , money]) => {
        try { return [key, parseScenarioInput(key, inputs[key], money)] }
        catch (error) { throw new Error(`${label}: ${error instanceof Error ? error.message : 'Invalid input.'}`) }
      }))
      setResult(await request('scenarios', 'POST', scenario) as ScenarioResult)
    })
  }
  async function loadCosts(offset = 0) {
    await action(async () => {
      validPeriod()
      // Query is constructed after validating the base; never embedded in settings.
      const url = financeURL(gatewayUrl, 'costs', import.meta.env.DEV)
      url.search = new URLSearchParams({ period, limit: '50', offset: String(offset) }).toString()
      if (!sessionToken.trim()) throw new Error('Add your admin session token in the gateway settings above.')
      const data = await requestJSON(url, sessionToken, { signal: session.scope.signal })
      setReport(data as CostReport)
    })
  }
  async function recordCost(event: FormEvent) {
    event.preventDefault(); setSaved(null)
    await action(async () => {
      validPeriod(); const amountCents = parseMoney(amount)
      const key = costKey || crypto.randomUUID()
      setCostKey(key) // retain on timeout/failure; identical retries only
      setSaved(await request('costs', 'POST', { period, category, amountCents }, key) as Cost)
      setCostKey(''); setReport(null)
    })
  }

  return <section className="panel finance-panel" id="finance-planning" aria-labelledby="finance-heading">
    <div className="eyebrow">ESTIMATES &amp; MANUAL COSTS · ADMIN ONLY</div>
    <h2 id="finance-heading">Finance planning</h2>
    <p className="notice">Illustrative assumptions, not official prices or approved driver rates. No checkout changes, earned wages or payouts. Uses the trusted gateway and in-memory admin token above.</p>
    <div className="overview-grid estimate-summary" aria-label="Scenario summary" aria-live="polite">
      <article className="overview-card"><div className="eyebrow">FORECAST / BEFORE COSTS</div><h3>{result ? dollars(result.platformRevenueForecastCents) : 'Not calculated'}</h3><p>Customer fees + assumed subscriptions. Not actual income.</p></article>
      <article className="overview-card"><div className="eyebrow">ESTIMATED NET / MONTH</div><h3>{result ? dollars(result.netOperatingCents) : 'No estimate yet'}</h3><p>After modeled worker and platform costs. Not a cash balance.</p></article>
      <article className="overview-card"><div className="eyebrow">BREAK-EVEN / CHECKOUTS</div><h3>{result ? result.breakEvenCheckouts ?? 'Not achievable' : 'Set assumptions'}</h3><p>{result?.breakEvenCheckouts === null ? 'Contribution is zero or negative.' : 'Calculated from the scenario, not an operating target.'}</p></article>
    </div>
    <form onSubmit={calculate} onInvalidCapture={event => { const section = (event.target as HTMLElement).closest('details'); if (section) section.open = true }}>
      <fieldset disabled={busy}>
        <legend>Monthly scenario — estimates only</legend>
        {scenarioSections.map((section, index) => <details className="form-section" key={section.title} open={index === 0 ? true : undefined}>
          <summary>{section.title}<span>{section.indexes.length} assumptions</span></summary>
          <p>{section.hint}</p>
          <div className="finance-inputs">{section.indexes.map(fieldIndex => {
            const [key, label, , money] = fields[fieldIndex]
            return <label key={key}>{label}<input required inputMode={money || key === 'pickupRadiusMilliMiles' ? 'decimal' : 'numeric'} value={inputs[key]} onChange={event => { setInputs({ ...inputs, [key]: event.target.value }); setResult(null) }} /></label>
          })}</div>
        </details>)}
        <details className="form-section calculation-help"><summary>How costs and rounding work</summary>
        <p>Money: $0–$1,000,000, up to two decimals. Counts/distance/time: 0–100,000; drops: 1–100. Processing excess is only the platform cost remaining after merchant allocation; do not enter the full processing fee twice. Do not add ledger totals on top of costs already in this scenario.</p>
        <p>A fixed base platform/service fee is a transparent planning policy; its amount is an assumption. The existing $2 service fee and $600 hosting are illustrations, not live rates or a hosting promise. Charge modeling counts one delivery fee and one service fee per checkout, plus optional priority fees and the fixed extra pickup platform rule below. There is no second maintenance customer fee: maintenance/support is a recurring platform cost. Its configurable $0 default means no approved budget, not zero actual cost. Keep support staffing in payroll and exclude it from maintenance/other overhead to avoid double counting.</p>
        <p>Each driver extra pickup per bundle adds 50% of original pickup pay, rounded half up to cents; the original and extras are added to the base, which must exclude pickup pay. This bundle count is NOT charged to each checkout. Start with 0.250 miles in dense cities; 0.500 is the configurable outer cap. Measure every extra store from the FIRST pickup, never a chain. Radius is metadata, not route eligibility.</p>
        <p>Separate platform rule: fixed 25 basis points (0.25%, NOT 25%) of merchandise subtotal per extra pickup per checkout. The first pickup is included; same-store orders count once. Round subtotal × 25 × extra pickups ÷ 10000 HALF UP once: $50 with one extra = $0.13; $100 with two extras = $0.50. Delivery/service/priority fees, tax and tips are excluded from the basis. Multiple drops per bundle do not change this per-checkout count. This form only estimates; actual checkout pricing uses server-owned orders behind a separate default-disabled Stripe-test-only flag. No live charges or new driver wages/payouts.</p>
        <p>Shoppers are separate people who prepare orders for driver pickup; cost is per checkout, not automatically per pickup. Optional fast shopping/delivery is a planning forecast, not guaranteed times or a live checkout configuration. Existing test-only priority is separate and unchanged. Uptake is a forecast: 100 basis points = 1%. All new money assumptions start at zero.</p>
        <p>Expected total fast fees round DOWN per checkout; each expected shopper/driver premium rounds UP per checkout before monthly multiplication. This conservatively changes each weighted amount by less than one cent per checkout. Do not duplicate shopper costs in other variable costs or payroll overhead.</p>
        </details>
        <button className="submit-button" type="submit">{busy ? 'Calculating…' : 'Calculate estimate'}</button>
      </fieldset>
    </form>
    {result && <div className="finance-results" role="status">
      <h3>Estimated monthly result — not verified revenue</h3>
      <p>Summary above; expand for the included fee and cost breakdown. Do not add included lines a second time.</p>
      <details className="form-section"><summary>Review full cost breakdown</summary>
      <dl>{([
        ['Average customer fees per checkout (includes expected fast fees)', result.customerFeesPerCheckoutCents],
        ['Customer fees / month (includes expected fast fees)', result.customerFeesMonthCents],
        ['Extra pickup platform fee / checkout (already included)', result.additionalPickupPlatformFeePerCheckoutCents],
        ['Extra pickup platform fee / month (already included)', result.additionalPickupPlatformFeeMonthCents],
        ['Platform/service fees / month (already included in customer fees)', result.platformServiceFeesMonthCents],
        ['Delivery fees / month (already included in customer fees)', result.deliveryFeesMonthCents],
        ['Expected fast fee revenue / month (included above)', result.fastFeesMonthCents],
        ['Assumed merchant subscriptions / month', result.subscriptionForecastCents],
        ['Each additional pickup estimate', result.additionalPickupCents],
        ['Additional pickup addon per bundle', result.pickupAddonBundleCents],
        ['Original + additional pickup total per bundle', result.pickupBundleCents],
        ['Driver bundle estimate (excludes per-checkout fast premium)', result.driverBundleCents],
        ['Driver per checkout (rounded display)', result.driverPerCheckoutCents],
        ['Driver monthly estimate (includes fast premium)', result.driverMonthCents],
        ['Expected fast driver premium / month (included above)', result.fastDriverPremiumMonthCents],
        ['Separate shopper monthly estimate (includes fast premium)', result.shopperMonthCents],
        ['Expected fast shopper premium / month (included above)', result.fastShopperPremiumMonthCents],
        ['Contribution per checkout (rounded display)', result.contributionPerCheckoutCents],
        ['Monthly contribution', result.contributionMonthCents],
        ['Monthly fixed hosting + maintenance + overhead + payroll', result.fixedMonthCents],
        ['Monthly maintenance/support (already included in fixed costs)', result.maintenanceMonthCents],
        ['Customer fees + subscriptions forecast (before costs, not net actual revenue)', result.platformRevenueForecastCents],
        ['Net operating estimate', result.netOperatingCents],
      ] as const).map(([label, cents]) => <div key={label}><dt>{label}</dt><dd>{dollars(cents)}</dd></div>)}</dl>
      <p>Break-even monthly checkouts: <strong>{result.breakEvenCheckouts === null ? 'None: delivery contribution is zero or negative' : result.breakEvenCheckouts}</strong></p>
      <p>Fixed extra pickup platform rate: {result.additionalPickupPlatformFeeBasisPoints} basis points = 0.25% per extra location per checkout. Included once in fees, forecast revenue and contribution; no driver payment record is created.</p>
      <p>Net operating margin: {result.netMarginBasisPoints === null ? 'Undefined: zero platform revenue' : `${result.netMarginBasisPoints} basis points (100 basis points = 1%)`}. Denominator is forecast customer fees including expected fast fees + assumed subscriptions only; excludes merchandise, tax and tips.</p>
      <p>Income forecasts, worker cost estimates and platform operating costs are separate. Fee and maintenance breakdowns are already included, never added twice. Existing test-only priority worker allocations are intentions, not platform profit, earned wages or automatic payouts; base service fees remain the platform fee, not automatic worker pay.</p>
      <p>Bundle distance/time charges round to the nearest cent; monthly regular amortized driver cost rounds up once, then expected per-checkout fast premiums are added. Break-even uses exact contribution under the conservative fast rounding policy ({result.contributionNumeratorCents} cents / {result.contributionDenominator}), not rounded display values.</p>
      </details>
    </div>}
    <hr />
    <h3>Manual cost ledger — neither earned nor paid</h3>
    <p>Append-only expense observations, not payroll approvals or proof of payment. No automatic import into estimates. Check for existing entries before recording; a new UUID records a new cost. No edits, deletes or reversal entries in this first version.</p>
    <p>Record maintenance/support expenses under existing “other”; support staffing under “staff”. Do not duplicate either cost in scenario maintenance, overhead or payroll.</p>
    <form onSubmit={recordCost}>
      <fieldset disabled={busy}>
        <legend>Record a nonnegative manual cost (USD)</legend>
        <div className="finance-inputs">
          <label>UTC accounting period<input required type="month" min="2000-01" max="2100-12" value={period} onChange={event => { setPeriod(event.target.value); setCostKey(''); setReport(null); setSaved(null) }} /></label>
          <label>Category<select value={category} onChange={event => { setCategory(event.target.value); setCostKey(''); setSaved(null) }}>{['driver', 'staff', 'hosting', 'processing_excess', 'refund_loss', 'other'].map(item => <option key={item} value={item}>{item === 'staff' ? 'staff (including shoppers)' : item.replace('_', ' ')}</option>)}</select></label>
          <label>Manual cost $<input required inputMode="decimal" value={amount} onChange={event => { setAmount(event.target.value); setCostKey(''); setSaved(null) }} /></label>
        </div>
        <button className="submit-button" type="submit">Record manual cost — no payment</button>
        {costKey && <p>Retry key retained in memory: {costKey}. Retry unchanged after a network error; changing the payload starts a new record.</p>}
      </fieldset>
    </form>
    {saved && <p role="status">Manual cost recorded: {dollars(saved.amountCents)} · {saved.id}. This does not establish earned compensation or a paid status.</p>}
    <button type="button" disabled={busy} onClick={() => { setReport(null); void loadCosts() }}>Load this month’s cost report</button>
    {report && <div>
      <p>Total manual costs for {report.period}: <strong>{dollars(report.totalCents)}</strong> (all records, not just this page). Not verified cash payments.</p>
      <div className="finance-table"><table><caption>Manual records — page starting at {report.offset + 1}</caption><thead><tr><th scope="col">Recorded (UTC)</th><th scope="col">Category</th><th scope="col">Cost</th><th scope="col">Actor / record</th></tr></thead><tbody>{report.costs.map(cost => <tr key={cost.id}><td>{cost.createdAt}</td><td>{cost.category}</td><td>{dollars(cost.amountCents)}</td><td>{cost.actor}<br />{cost.id}</td></tr>)}</tbody></table></div>
      {!report.costs.length && <p>No records on this page.</p>}
      <button type="button" disabled={busy || report.offset === 0} onClick={() => void loadCosts(Math.max(0, report.offset - 50))}>Previous page</button>
      <button type="button" disabled={busy || report.costs.length < 50 || report.offset >= 100000} onClick={() => void loadCosts(report.offset + 50)}>Next page</button>
    </div>}
    {error && <p className="alert alert-error" role="alert">{error}</p>}
    {busy && <p role="status">Loading finance request…</p>}
  </section>
}