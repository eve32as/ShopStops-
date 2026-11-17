// Synthetic OpenAPI-shaped responses only. No gateway, identity provider or Stripe.
export const gateway = 'https://acceptance.shopstops.example/v1'
export const syntheticToken = 'synthetic-acceptance-admin-not-a-credential'
export const orderId = '11111111-1111-4111-8111-111111111111'
export const createdAt = '2026-11-17T12:00:00Z'

export function taskPage(tasks) {
  return { tasks, limit: 50, offset: 0 }
}

export function manualCost(id) {
  return { id, kind: 'manual_cost', period: '2026-11', category: 'hosting', amountCents: 501, actor: 'synthetic_admin', createdAt }
}

export function costReport(cost) {
  return { kind: 'manual_cost_report', period: cost.period, totalCents: '501', limit: 50, offset: 0, costs: [cost] }
}

// Matches the default form scenario; values are returned by the simulated API,
// never calculated in the test browser. Backend arithmetic has its own tests.
export const scenario = {
  kind: 'estimate', additionalPickupPlatformFeeBasisPoints: 25,
  additionalPickupPlatformFeePerCheckoutCents: 0, additionalPickupPlatformFeeMonthCents: 0,
  customerFeesPerCheckoutCents: 800, customerFeesMonthCents: 800000,
  platformServiceFeesMonthCents: 200000, deliveryFeesMonthCents: 600000,
  subscriptionForecastCents: 0, driverBundleCents: 500, driverPerCheckoutCents: 500,
  driverMonthCents: 500000, additionalPickupCents: 0, pickupAddonBundleCents: 0,
  pickupBundleCents: 0, pickupRadiusMilliMiles: 500, shopperPerCheckoutCents: 0,
  shopperMonthCents: 0, fastFeesPerCheckoutCents: 0, fastFeesMonthCents: 0,
  fastShopperPremiumPerCheckoutCents: 0, fastShopperPremiumMonthCents: 0,
  fastDriverPremiumPerCheckoutCents: 0, fastDriverPremiumMonthCents: 0,
  contributionPerCheckoutCents: 300, contributionNumeratorCents: 300,
  contributionDenominator: 1, contributionMonthCents: 300000, fixedMonthCents: 60000,
  maintenanceMonthCents: 0, platformRevenueForecastCents: 800000,
  netOperatingCents: 240000, netMarginBasisPoints: 3000, breakEvenCheckouts: 200,
}

export function assignedTask(id) {
  return {
    id, orderId, shopperActor: 'synthetic_worker', assignedBy: 'synthetic_admin',
    merchantId: '22222222-2222-4222-8222-222222222222', completionAmountCents: 501,
    currency: 'usd', jurisdictionRef: 'synthetic_jurisdiction', eligibilityStatus: 'pending_review',
    paymentStatusAtAssignment: 'not_recorded', createdAt, status: 'assigned',
    completion: null, earnings: null,
  }
}

export function completedTask(id) {
  return {
    ...assignedTask(id), status: 'verified_completed',
    completion: {
      verifiedBy: 'synthetic_admin', itemsConfirmed: true, substitutionsConfirmed: true,
      receiptRef: 'synthetic_receipt', handoffRef: 'synthetic_handoff',
      paymentStatusAtCompletion: 'not_recorded', createdAt,
    },
    earnings: { kind: 'earned_per_order', status: 'pending_review', completionAmountCents: 501, currency: 'usd', createdAt },
  }
}