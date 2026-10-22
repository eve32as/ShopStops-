package finance

import (
	"errors"
	"math"
	"sort"
)

const maxInvoiceLineCents = int64(math.MaxInt32)

type Assumptions struct {
	TypicalBasketCents            int64
	TargetMarginBasisPoints       int64
	StripeFeeBasisPoints          int64
	StripeFixedFeeCents           int64
	StandardCommissionBasisPoints int64
	PremiumCommissionBasisPoints  int64
	DriverBasePayoutCents         int64
	AlongRoutePickupBonusCents    int64
	FixedOrderCostCents           int64
}

func DefaultAssumptions() Assumptions {
	return Assumptions{
		TypicalBasketCents:            4500,
		TargetMarginBasisPoints:       1500,
		StripeFeeBasisPoints:          290,
		StripeFixedFeeCents:           30,
		StandardCommissionBasisPoints: 1000,
		PremiumCommissionBasisPoints:  500,
		DriverBasePayoutCents:         700,
		AlongRoutePickupBonusCents:    450,
		FixedOrderCostCents:           120,
	}
}

type MerchantBasket struct {
	MerchantID      string
	ItemSubtotal    int64
	PremiumMerchant bool
}

type Request struct {
	Merchants             []MerchantBasket
	AlongRoutePickupCount int
	PassThroughCents      int64
}

type MerchantFee struct {
	MerchantID      string
	ItemSubtotal    int64
	CommissionBPS   int64
	CommissionCents int64
}

type Quote struct {
	ItemSubtotalCents       int64
	MerchantFees            []MerchantFee
	CommissionCents         int64
	DeliveryFeeCents        int64
	ServiceFeeCents         int64
	EstimatedProcessorCents int64
	DriverPayoutCents       int64
	FixedOrderCostCents     int64
	TargetProfitCents       int64
	ProjectedNetProfitCents int64
	AlongRoutePickupCount   int
	PassThroughCents        int64
}

type MileageBill struct {
	DriverMileageCostCents int64
	CustomerSurchargeCents int64
}

func CalculateMileage(rateCentsPerMile int64, distanceMiles float64) (MileageBill, error) {
	if rateCentsPerMile < 0 || rateCentsPerMile > 100000 ||
		math.IsNaN(distanceMiles) || math.IsInf(distanceMiles, 0) ||
		distanceMiles < 0 {
		return MileageBill{}, errors.New("invalid mileage pricing inputs")
	}

	costCents := distanceMiles * float64(rateCentsPerMile)
	if costCents > float64(maxInvoiceLineCents)+0.5 {
		return MileageBill{}, errors.New("driver mileage cost exceeds supported invoice amount")
	}
	costCents = math.Round(costCents)
	surchargeCents := (int64(costCents)*100 + 42) / 85
	if surchargeCents > maxInvoiceLineCents {
		return MileageBill{}, errors.New("customer mileage surcharge exceeds supported invoice amount")
	}
	return MileageBill{
		DriverMileageCostCents: int64(costCents),
		CustomerSurchargeCents: surchargeCents,
	}, nil
}

func Calculate(assumptions Assumptions, request Request) (Quote, error) {
	if err := validateAssumptions(assumptions); err != nil {
		return Quote{}, err
	}
	if len(request.Merchants) == 0 || len(request.Merchants) > 100 ||
		request.AlongRoutePickupCount < 0 || request.AlongRoutePickupCount > 100 ||
		request.PassThroughCents < 0 {
		return Quote{}, errors.New("invalid checkout pricing inputs")
	}

	quote := Quote{
		MerchantFees:          make([]MerchantFee, 0, len(request.Merchants)),
		AlongRoutePickupCount: request.AlongRoutePickupCount,
		PassThroughCents:      request.PassThroughCents,
	}
	seen := make(map[string]struct{}, len(request.Merchants))
	for _, merchant := range request.Merchants {
		if merchant.MerchantID == "" || merchant.ItemSubtotal <= 0 ||
			merchant.ItemSubtotal > maxInvoiceLineCents {
			return Quote{}, errors.New("merchant basket amounts must be positive invoice-sized values")
		}
		if _, exists := seen[merchant.MerchantID]; exists {
			return Quote{}, errors.New("merchant baskets must be unique")
		}
		seen[merchant.MerchantID] = struct{}{}
		if quote.ItemSubtotalCents > maxInvoiceLineCents-merchant.ItemSubtotal {
			return Quote{}, errors.New("checkout basket exceeds supported invoice total")
		}
		quote.ItemSubtotalCents += merchant.ItemSubtotal
		rate := assumptions.StandardCommissionBasisPoints
		if merchant.PremiumMerchant {
			rate = assumptions.PremiumCommissionBasisPoints
		}
		commission := roundBasisPoints(merchant.ItemSubtotal, rate)
		quote.CommissionCents += commission
		quote.MerchantFees = append(quote.MerchantFees, MerchantFee{
			MerchantID: merchant.MerchantID, ItemSubtotal: merchant.ItemSubtotal,
			CommissionBPS: rate, CommissionCents: commission,
		})
	}
	sort.Slice(quote.MerchantFees, func(i, j int) bool {
		return quote.MerchantFees[i].MerchantID < quote.MerchantFees[j].MerchantID
	})
	if quote.CommissionCents > maxInvoiceLineCents {
		return Quote{}, errors.New("merchant commissions exceed supported invoice amounts")
	}

	quote.DriverPayoutCents = assumptions.DriverBasePayoutCents +
		int64(request.AlongRoutePickupCount)*assumptions.AlongRoutePickupBonusCents
	quote.FixedOrderCostCents = assumptions.FixedOrderCostCents
	quote.TargetProfitCents = ceilBasisPoints(
		quote.ItemSubtotalCents, assumptions.TargetMarginBasisPoints,
	)

	shopperFees, err := minimumShopperFees(assumptions, quote)
	if err != nil {
		return Quote{}, err
	}
	quote.DeliveryFeeCents = min(shopperFees, quote.DriverPayoutCents)
	quote.ServiceFeeCents = shopperFees - quote.DeliveryFeeCents
	quote.EstimatedProcessorCents = estimateProcessorFee(
		assumptions, quote.ItemSubtotalCents+shopperFees+request.PassThroughCents,
	)
	quote.ProjectedNetProfitCents = quote.CommissionCents + shopperFees -
		quote.EstimatedProcessorCents - quote.DriverPayoutCents -
		quote.FixedOrderCostCents
	if quote.DeliveryFeeCents > maxInvoiceLineCents ||
		quote.ServiceFeeCents > maxInvoiceLineCents ||
		quote.ItemSubtotalCents+shopperFees+request.PassThroughCents > maxInvoiceLineCents {
		return Quote{}, errors.New("checkout total exceeds supported payment amount")
	}
	if quote.ProjectedNetProfitCents < quote.TargetProfitCents {
		return Quote{}, errors.New("pricing could not reach the configured target margin")
	}
	return quote, nil
}

func minimumShopperFees(assumptions Assumptions, quote Quote) (int64, error) {
	maxFees := maxInvoiceLineCents - quote.ItemSubtotalCents - quote.PassThroughCents
	if maxFees < 0 {
		return 0, errors.New("checkout total exceeds supported payment amount")
	}
	profitAt := func(fees int64) int64 {
		processing := estimateProcessorFee(
			assumptions, quote.ItemSubtotalCents+fees+quote.PassThroughCents,
		)
		return quote.CommissionCents + fees - processing -
			quote.DriverPayoutCents - quote.FixedOrderCostCents
	}
	if profitAt(maxFees) < quote.TargetProfitCents {
		return 0, errors.New("target margin is not attainable within payment limits")
	}
	low, high := int64(0), maxFees
	for low < high {
		middle := low + (high-low)/2
		if profitAt(middle) >= quote.TargetProfitCents {
			high = middle
		} else {
			low = middle + 1
		}
	}
	return low, nil
}

func estimateProcessorFee(assumptions Assumptions, amountCents int64) int64 {
	return roundBasisPoints(amountCents, assumptions.StripeFeeBasisPoints) +
		assumptions.StripeFixedFeeCents
}

func roundBasisPoints(amount, basisPoints int64) int64 {
	return (amount*basisPoints + 5000) / 10000
}

func ceilBasisPoints(amount, basisPoints int64) int64 {
	return (amount*basisPoints + 9999) / 10000
}

func validateAssumptions(assumptions Assumptions) error {
	if assumptions.TypicalBasketCents <= 0 ||
		assumptions.TargetMarginBasisPoints < 0 ||
		assumptions.TargetMarginBasisPoints >= 10000 ||
		assumptions.StripeFeeBasisPoints < 0 ||
		assumptions.StripeFeeBasisPoints >= 10000 ||
		assumptions.StripeFixedFeeCents < 0 ||
		assumptions.StandardCommissionBasisPoints < 0 ||
		assumptions.StandardCommissionBasisPoints > 10000 ||
		assumptions.PremiumCommissionBasisPoints < 0 ||
		assumptions.PremiumCommissionBasisPoints > 10000 ||
		assumptions.DriverBasePayoutCents < 0 ||
		assumptions.AlongRoutePickupBonusCents < 0 ||
		assumptions.FixedOrderCostCents < 0 {
		return errors.New("invalid financial assumptions")
	}
	return nil
}
