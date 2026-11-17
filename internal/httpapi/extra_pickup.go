package httpapi

import (
	"errors"
	"strings"
)

// ExtraPickupConfig cannot be supplied by a checkout customer. The rate is fixed.
type ExtraPickupConfig struct{ enabled bool }

func ExtraPickupConfigFromEnv(getenv func(string) string) (ExtraPickupConfig, error) {
	flag := getenv("SHOPSTOPS_EXTRA_PICKUP_TEST_ONLY")
	if flag == "" || flag == "false" {
		return ExtraPickupConfig{}, nil
	}
	if flag != "true" {
		return ExtraPickupConfig{}, errors.New("SHOPSTOPS_EXTRA_PICKUP_TEST_ONLY must be true or false")
	}
	if !testStripeKey.MatchString(getenv("STRIPE_SECRET_KEY")) {
		return ExtraPickupConfig{}, errors.New("extra pickup pricing requires a Stripe test secret key")
	}
	if strings.TrimSpace(getenv("STRIPE_WEBHOOK_SECRET")) == "" {
		return ExtraPickupConfig{}, errors.New("extra pickup pricing requires a Stripe webhook secret")
	}
	return ExtraPickupConfig{enabled: true}, nil
}

// WithExtraPickupPricing preserves the existing optional priority constructor.
func (p *PostgresRepository) WithExtraPickupPricing(c ExtraPickupConfig) *PostgresRepository {
	p.extraPickup = c
	return p
}

type extraPickupSnapshot struct {
	TestOnly                 bool  `json:"testOnly"`
	MerchandiseSubtotalCents int64 `json:"merchandiseSubtotalCents"`
	AdditionalPickups        int   `json:"additionalPickups"`
	RateBasisPoints          int   `json:"rateBasisPoints"`
	AmountCents              int64 `json:"amountCents"`
}
