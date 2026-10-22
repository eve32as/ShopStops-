package finance

import "testing"

func TestCalculateBaselineMerchantTiers(t *testing.T) {
	tests := []struct {
		name              string
		premium           bool
		commissionCents   int64
		serviceFeeCents   int64
		processorFeeCents int64
		wantDelivery      int64
		wantServiceFloor  int64
	}{
		{
			name: "standard", commissionCents: 450, serviceFeeCents: 541,
			processorFeeCents: 196, wantDelivery: 700,
		},
		{
			name: "premium", premium: true, commissionCents: 225,
			serviceFeeCents: 773, processorFeeCents: 203, wantDelivery: 700,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quote, err := Calculate(DefaultAssumptions(), Request{
				Merchants: []MerchantBasket{{
					MerchantID: "merchant-1", ItemSubtotal: 4500,
					PremiumMerchant: test.premium,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if quote.CommissionCents != test.commissionCents ||
				quote.DeliveryFeeCents != test.wantDelivery ||
				quote.ServiceFeeCents != test.serviceFeeCents ||
				quote.ProjectedNetProfitCents < quote.TargetProfitCents {
				t.Fatalf("unexpected baseline quote: %+v", quote)
			}
			if quote.EstimatedProcessorCents != test.processorFeeCents {
				t.Fatalf("estimated processor fee = %d, want %d",
					quote.EstimatedProcessorCents, test.processorFeeCents)
			}
		})
	}
}

func TestCalculateAddsAlongRouteCompensationAndPassThrough(t *testing.T) {
	base, err := Calculate(DefaultAssumptions(), Request{
		Merchants: []MerchantBasket{{
			MerchantID: "merchant-1", ItemSubtotal: 4500,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	withPickup, err := Calculate(DefaultAssumptions(), Request{
		Merchants: []MerchantBasket{{
			MerchantID: "merchant-1", ItemSubtotal: 4500,
		}},
		AlongRoutePickupCount: 1,
		PassThroughCents:      20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if withPickup.DriverPayoutCents != base.DriverPayoutCents+450 {
		t.Fatalf("driver payout = %d, want %d", withPickup.DriverPayoutCents, base.DriverPayoutCents+450)
	}
	if withPickup.DeliveryFeeCents+withPickup.ServiceFeeCents <=
		base.DeliveryFeeCents+base.ServiceFeeCents {
		t.Fatal("additional stop should increase shopper fees to preserve the target margin")
	}
	if withPickup.ProjectedNetProfitCents < withPickup.TargetProfitCents {
		t.Fatalf("projected margin missed target: %+v", withPickup)
	}
}

func TestCalculateRejectsInvalidOrUnattainableQuote(t *testing.T) {
	tests := []Request{
		{},
		{Merchants: []MerchantBasket{{MerchantID: "", ItemSubtotal: 4500}}},
		{Merchants: []MerchantBasket{{MerchantID: "m1", ItemSubtotal: 0}}},
		{Merchants: []MerchantBasket{
			{MerchantID: "m1", ItemSubtotal: 4500},
			{MerchantID: "m1", ItemSubtotal: 100},
		}},
	}
	for i, request := range tests {
		if _, err := Calculate(DefaultAssumptions(), request); err == nil {
			t.Errorf("case %d unexpectedly succeeded", i)
		}
	}

	assumptions := DefaultAssumptions()
	assumptions.TargetMarginBasisPoints = 9900
	if _, err := Calculate(assumptions, Request{
		Merchants: []MerchantBasket{{MerchantID: "m1", ItemSubtotal: maxInvoiceLineCents}},
	}); err == nil {
		t.Fatal("unattainable target unexpectedly succeeded")
	}
}
