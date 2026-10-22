package finance

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestProductionPricingVerification(t *testing.T) {
	engine := NewProductionPricingEngine(65, true, false)
	bill, err := engine.ComputeProductionInvoice(context.Background(), 4500, 5)
	if err != nil {
		t.Fatal(err)
	}
	if bill.DriverMileageCost != 325 {
		t.Errorf("DriverMileageCost = %d, want 325", bill.DriverMileageCost)
	}
	if bill.CustomerSurcharge != 382 {
		t.Errorf("CustomerSurcharge = %d, want 382", bill.CustomerSurcharge)
	}
	if bill.ProcessingFee != 161 {
		t.Errorf("ProcessingFee = %d, want 161", bill.ProcessingFee)
	}
	if bill.DriverBasePay != 700 || bill.PlatformProtection != 120 {
		t.Errorf("base pay/protection = %d/%d, want 700/120",
			bill.DriverBasePay, bill.PlatformProtection)
	}
	if bill.NetPlatformMargin != 732 {
		t.Errorf("NetPlatformMargin = %d, want 732", bill.NetPlatformMargin)
	}
	if bill.TotalCharged != 5863 {
		t.Errorf("TotalCharged = %d, want 5863", bill.TotalCharged)
	}
}

func TestAlongRouteAndNonSubscribedPricing(t *testing.T) {
	engine := NewProductionPricingEngine(65, false, true)
	bill, err := engine.ComputeProductionInvoice(context.Background(), 4500, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bill.DriverBasePay != 450 {
		t.Errorf("DriverBasePay = %d, want 450", bill.DriverBasePay)
	}
	if bill.MerchantSurcharge != 225 {
		t.Errorf("MerchantSurcharge = %d, want 225", bill.MerchantSurcharge)
	}
	if bill.TotalCharged != 5231 {
		t.Errorf("TotalCharged = %d, want 5231", bill.TotalCharged)
	}
}

func TestProductionPricingRejectsInvalidOrOverflowingInputs(t *testing.T) {
	tests := []struct {
		name     string
		engine   *ProductionPricingEngine
		basket   int64
		distance float64
	}{
		{name: "negative basket", engine: NewProductionPricingEngine(65, true, false), basket: -1},
		{name: "negative rate", engine: NewProductionPricingEngine(-1, true, false)},
		{name: "negative miles", engine: NewProductionPricingEngine(65, true, false), distance: -1},
		{name: "NaN miles", engine: NewProductionPricingEngine(65, true, false), distance: math.NaN()},
		{name: "infinite miles", engine: NewProductionPricingEngine(65, true, false), distance: math.Inf(1)},
		{name: "mileage overflow", engine: NewProductionPricingEngine(math.MaxInt64, true, false), distance: 2},
		{name: "total overflow", engine: NewProductionPricingEngine(0, true, false), basket: math.MaxInt64},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.engine.ComputeProductionInvoice(context.Background(), test.basket, test.distance)
			if !errors.Is(err, ErrInvalidPricingInput) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidPricingInput)
			}
		})
	}
}

func TestProductionPricingHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewProductionPricingEngine(65, true, false).
		ComputeProductionInvoice(ctx, 100, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
