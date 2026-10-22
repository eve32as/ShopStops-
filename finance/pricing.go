package finance

import (
	"context"
	"errors"
	"math"
	"math/big"
)

const (
	standardDriverBasePayCents = int64(700)
	alongRouteBonusCents       = int64(450)
	platformProtectionCents    = int64(120)
)

var ErrInvalidPricingInput = errors.New("invalid pricing input")

type FinalizedBill struct {
	BasketTotal        int64 `json:"basket_total"`
	ProcessingFee      int64 `json:"processing_fee"`
	DriverBasePay      int64 `json:"driver_base_pay"`
	DriverMileageCost  int64 `json:"driver_mileage_cost"`
	CustomerSurcharge  int64 `json:"customer_surcharge"`
	PlatformProtection int64 `json:"platform_protection"`
	MerchantSurcharge  int64 `json:"merchant_surcharge"`
	NetPlatformMargin  int64 `json:"net_platform_margin"`
	TotalCharged       int64 `json:"total_charged"`
}

type ProductionPricingEngine struct {
	DriverMileageRateCents int64
	IsMerchantSubscribed   bool
	IsAlongRouteStop       bool
}

func NewProductionPricingEngine(
	zoneRateCents int64, subscribed, alongRoute bool,
) *ProductionPricingEngine {
	return &ProductionPricingEngine{
		DriverMileageRateCents: zoneRateCents,
		IsMerchantSubscribed:   subscribed,
		IsAlongRouteStop:       alongRoute,
	}
}

func (e *ProductionPricingEngine) ComputeProductionInvoice(
	ctx context.Context, basketCents int64, distanceMiles float64,
) (FinalizedBill, error) {
	if ctx == nil || e == nil || basketCents < 0 ||
		e.DriverMileageRateCents < 0 || math.IsNaN(distanceMiles) ||
		math.IsInf(distanceMiles, 0) || distanceMiles < 0 {
		return FinalizedBill{}, ErrInvalidPricingInput
	}
	if err := ctx.Err(); err != nil {
		return FinalizedBill{}, err
	}

	bill := FinalizedBill{BasketTotal: basketCents, PlatformProtection: platformProtectionCents}
	if e.IsAlongRouteStop {
		bill.DriverBasePay = alongRouteBonusCents
	} else {
		bill.DriverBasePay = standardDriverBasePayCents
	}

	mileageCost := distanceMiles * float64(e.DriverMileageRateCents)
	if math.IsInf(mileageCost, 0) || math.IsNaN(mileageCost) ||
		mileageCost >= float64(math.MaxInt64) {
		return FinalizedBill{}, ErrInvalidPricingInput
	}
	bill.DriverMileageCost = int64(math.Round(mileageCost))

	var err error
	if bill.CustomerSurcharge, err = roundRatio(bill.DriverMileageCost, 100, 85); err != nil {
		return FinalizedBill{}, err
	}
	if bill.ProcessingFee, err = roundRatio(basketCents, 29, 1000); err != nil {
		return FinalizedBill{}, err
	}
	if bill.ProcessingFee, err = checkedAdd(bill.ProcessingFee, 30); err != nil {
		return FinalizedBill{}, err
	}
	if !e.IsMerchantSubscribed {
		if bill.MerchantSurcharge, err = roundRatio(basketCents, 5, 100); err != nil {
			return FinalizedBill{}, err
		}
	}

	basePlatformShare, err := roundRatio(basketCents, 15, 100)
	if err != nil {
		return FinalizedBill{}, err
	}
	mileageSpread := bill.CustomerSurcharge - bill.DriverMileageCost
	if bill.NetPlatformMargin, err = checkedAdd(basePlatformShare, mileageSpread); err != nil {
		return FinalizedBill{}, err
	}
	if bill.NetPlatformMargin, err = checkedAdd(bill.NetPlatformMargin, bill.MerchantSurcharge); err != nil {
		return FinalizedBill{}, err
	}

	bill.TotalCharged = bill.BasketTotal
	for _, amount := range []int64{
		bill.ProcessingFee, bill.DriverBasePay, bill.CustomerSurcharge, bill.PlatformProtection,
	} {
		if bill.TotalCharged, err = checkedAdd(bill.TotalCharged, amount); err != nil {
			return FinalizedBill{}, err
		}
	}
	return bill, nil
}

func roundRatio(value, numerator, denominator int64) (int64, error) {
	amount := new(big.Int).Mul(big.NewInt(value), big.NewInt(numerator))
	amount.Add(amount, big.NewInt(denominator/2))
	amount.Quo(amount, big.NewInt(denominator))
	if !amount.IsInt64() {
		return 0, ErrInvalidPricingInput
	}
	return amount.Int64(), nil
}

func checkedAdd(left, right int64) (int64, error) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, ErrInvalidPricingInput
	}
	if right < 0 && left < math.MinInt64-right {
		return 0, ErrInvalidPricingInput
	}
	return left + right, nil
}
