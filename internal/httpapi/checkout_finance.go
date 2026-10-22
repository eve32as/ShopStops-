package httpapi

import (
	"context"
	"database/sql"
	"errors"

	"github.com/eve32as/ShopStops-/internal/finance"
)

func (p *PostgresRepository) checkoutPricing(
	ctx context.Context, tx *sql.Tx, request checkoutRequest, customerRef string,
) (finance.Quote, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT o.id::text, o.item_subtotal_cents, s.merchant_id::text
		FROM orders o
		JOIN store_locations s ON s.id = o.store_location_id
		JOIN merchants m ON m.id = s.merchant_id
		WHERE o.id = ANY($1::uuid[]) AND o.customer_ref = $2
		ORDER BY o.id
		FOR SHARE OF o, s, m`, request.OrderIDs, customerRef)
	if err != nil {
		return finance.Quote{}, err
	}
	defer rows.Close()

	type merchantBasket struct {
		id       string
		subtotal int64
	}
	merchants := make(map[string]*merchantBasket)
	seenOrders := make(map[string]struct{}, len(request.OrderIDs))
	for rows.Next() {
		var orderID, merchantID string
		var subtotal int64
		if err := rows.Scan(&orderID, &subtotal, &merchantID); err != nil {
			return finance.Quote{}, err
		}
		seenOrders[orderID] = struct{}{}
		merchant, exists := merchants[merchantID]
		if !exists {
			merchant = &merchantBasket{id: merchantID}
			merchants[merchantID] = merchant
		}
		merchant.subtotal += subtotal
	}
	if err := rows.Err(); err != nil {
		return finance.Quote{}, err
	}
	if len(seenOrders) != len(request.OrderIDs) {
		return finance.Quote{}, ErrConflict
	}
	if err := rows.Close(); err != nil {
		return finance.Quote{}, err
	}

	inputs := make([]finance.MerchantBasket, 0, len(merchants))
	for _, merchant := range merchants {
		var premium bool
		err := tx.QueryRowContext(ctx, `
			SELECT COALESCE((
				(status = 'active'
				 AND (current_period_end IS NULL OR current_period_end > now()))
				OR (status = 'past_due'
				    AND payment_failed_at > now() - interval '48 hours')
			), false)
			FROM merchant_subscriptions
			WHERE merchant_id = $1
			FOR SHARE`, merchant.id,
		).Scan(&premium)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return finance.Quote{}, err
		}
		inputs = append(inputs, finance.MerchantBasket{
			MerchantID: merchant.id, ItemSubtotal: merchant.subtotal,
			PremiumMerchant: err == nil && premium,
		})
	}

	var passThroughCents int64
	if err := tx.QueryRowContext(ctx, `
		SELECT CASE WHEN EXISTS (
			SELECT 1 FROM jurisdictions
			WHERE code = 'US-DC' AND enabled
			  AND ST_Covers(
			      boundary,
			      ST_SetSRID(ST_MakePoint($1, $2), 4326)
			  )
		) THEN 20 ELSE 0 END`,
		request.Dropoff.Longitude, request.Dropoff.Latitude,
	).Scan(&passThroughCents); err != nil {
		return finance.Quote{}, err
	}

	quote, err := finance.Calculate(p.financials, finance.Request{
		Merchants: inputs, PassThroughCents: passThroughCents,
	})
	if err != nil {
		return finance.Quote{}, ErrIneligible
	}
	return quote, nil
}

func persistCheckoutPricing(
	ctx context.Context, tx *sql.Tx, checkoutID string,
	quote finance.Quote, assumptions finance.Assumptions,
) error {
	for _, merchant := range quote.MerchantFees {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO checkout_merchant_financials (
				checkout_id, merchant_id, item_gross_cents,
				commission_rate_bps, platform_fee_cents
			) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (checkout_id, merchant_id) DO NOTHING`,
			checkoutID, merchant.MerchantID, merchant.ItemSubtotal,
			merchant.CommissionBPS, merchant.CommissionCents,
		); err != nil {
			return err
		}
		var matches bool
		if err := tx.QueryRowContext(ctx, `
			SELECT item_gross_cents = $3
			   AND commission_rate_bps = $4
			   AND platform_fee_cents = $5
			FROM checkout_merchant_financials
			WHERE checkout_id = $1 AND merchant_id = $2`,
			checkoutID, merchant.MerchantID, merchant.ItemSubtotal,
			merchant.CommissionBPS, merchant.CommissionCents,
		).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return ErrConflict
		}
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO checkout_financial_estimates (
			checkout_id, item_subtotal_cents, commission_cents,
			delivery_fee_cents, service_fee_cents, pass_through_cents,
			estimated_processor_fee_cents, driver_payout_cents,
			fixed_order_cost_cents, target_profit_cents,
			projected_net_profit_cents, target_margin_bps,
			stripe_fee_bps, stripe_fixed_fee_cents,
			along_route_pickup_count, typical_basket_cents,
			driver_base_payout_cents, along_route_pickup_bonus_cents
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
			$15, $16, $17, $18
		)
		ON CONFLICT (checkout_id) DO NOTHING`,
		checkoutID, quote.ItemSubtotalCents, quote.CommissionCents,
		quote.DeliveryFeeCents, quote.ServiceFeeCents, quote.PassThroughCents,
		quote.EstimatedProcessorCents, quote.DriverPayoutCents,
		quote.FixedOrderCostCents, quote.TargetProfitCents,
		quote.ProjectedNetProfitCents, assumptions.TargetMarginBasisPoints,
		assumptions.StripeFeeBasisPoints, assumptions.StripeFixedFeeCents,
		quote.AlongRoutePickupCount, assumptions.TypicalBasketCents,
		assumptions.DriverBasePayoutCents, assumptions.AlongRoutePickupBonusCents,
	)
	if err != nil {
		return err
	}
	var matches bool
	err = tx.QueryRowContext(ctx, `
		SELECT item_subtotal_cents = $2
		   AND commission_cents = $3
		   AND delivery_fee_cents = $4
		   AND service_fee_cents = $5
		   AND pass_through_cents = $6
		   AND estimated_processor_fee_cents = $7
		   AND driver_payout_cents = $8
		   AND fixed_order_cost_cents = $9
		   AND target_profit_cents = $10
		   AND projected_net_profit_cents = $11
		   AND target_margin_bps = $12
		   AND stripe_fee_bps = $13
		   AND stripe_fixed_fee_cents = $14
		   AND along_route_pickup_count = $15
		   AND typical_basket_cents = $16
		   AND driver_base_payout_cents = $17
		   AND along_route_pickup_bonus_cents = $18
		FROM checkout_financial_estimates
		WHERE checkout_id = $1`,
		checkoutID, quote.ItemSubtotalCents, quote.CommissionCents,
		quote.DeliveryFeeCents, quote.ServiceFeeCents, quote.PassThroughCents,
		quote.EstimatedProcessorCents, quote.DriverPayoutCents,
		quote.FixedOrderCostCents, quote.TargetProfitCents,
		quote.ProjectedNetProfitCents, assumptions.TargetMarginBasisPoints,
		assumptions.StripeFeeBasisPoints, assumptions.StripeFixedFeeCents,
		quote.AlongRoutePickupCount, assumptions.TypicalBasketCents,
		assumptions.DriverBasePayoutCents, assumptions.AlongRoutePickupBonusCents,
	).Scan(&matches)
	if err != nil {
		return err
	}
	if !matches {
		return ErrConflict
	}
	return nil
}
