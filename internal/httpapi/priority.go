package httpapi

import (
	"bytes"
	"errors"
	"net/http"
	"regexp"
	"strconv"
)

const maxPriorityRateCents = 1000000

var ErrPriorityUnavailable = errors.New("priority service is unavailable")

type priorityChoice bool

func (c *priorityChoice) UnmarshalJSON(raw []byte) error {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		*c = true
	case "false":
		*c = false
	default:
		return errors.New("priorityService must be a boolean")
	}
	return nil
}

// PriorityConfig is constructed only from explicit operational configuration,
// never from customer requests or the finance scenario calculator.
type PriorityConfig struct {
	enabled       bool
	shoppingCents int
	deliveryCents int
}

var testStripeKey = regexp.MustCompile(`^sk_test_[A-Za-z0-9]+$`)

func PriorityConfigFromEnv(getenv func(string) string) (PriorityConfig, error) {
	flag := getenv("SHOPSTOPS_PRIORITY_TEST_ONLY")
	if flag == "" || flag == "false" {
		return PriorityConfig{}, nil
	}
	if flag != "true" {
		return PriorityConfig{}, errors.New("SHOPSTOPS_PRIORITY_TEST_ONLY must be true or false")
	}
	if !testStripeKey.MatchString(getenv("STRIPE_SECRET_KEY")) {
		return PriorityConfig{}, errors.New("priority requires a Stripe test secret key")
	}
	if getenv("STRIPE_WEBHOOK_SECRET") == "" {
		return PriorityConfig{}, errors.New("priority requires a Stripe webhook secret")
	}
	var c PriorityConfig
	c.enabled = true
	for i, name := range []string{"SHOPSTOPS_PRIORITY_SHOPPING_CENTS", "SHOPSTOPS_PRIORITY_DELIVERY_CENTS", "SHOPSTOPS_DELIVERY_FEE_CENTS", "SHOPSTOPS_SERVICE_FEE_CENTS"} {
		raw := getenv(name)
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || value < 0 || value > maxPriorityRateCents {
			return PriorityConfig{}, errors.New(name + " requires explicit integer cents in 0..1000000")
		}
		if i == 0 {
			c.shoppingCents = int(value)
		}
		if i == 1 {
			c.deliveryCents = int(value)
		}
	}
	if c.shoppingCents == 0 || c.deliveryCents == 0 {
		return PriorityConfig{}, errors.New("priority shopping and delivery surcharges must both be positive")
	}
	return c, nil
}

type prioritySnapshot struct {
	Selected               bool   `json:"selected"`
	TestOnly               bool   `json:"testOnly"`
	ShoppingCents          int    `json:"shoppingCents"`
	DeliveryCents          int    `json:"deliveryCents"`
	ShopperAllocationCents int    `json:"shopperAllocationCents"`
	DriverAllocationCents  int    `json:"driverAllocationCents"`
	AllocationStatus       string `json:"allocationStatus"`
}

func (p *PostgresRepository) PriorityPricing() PriorityConfig { return p.priority }

func (s *Server) priorityPricing(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "customer"); !ok {
		return
	}
	c := PriorityConfig{}
	if provider, ok := s.repo.(interface{ PriorityPricing() PriorityConfig }); ok {
		c = provider.PriorityPricing()
	}
	w.Header().Set("Cache-Control", "no-store")
	response := map[string]any{
		"available": c.enabled, "testOnly": true, "currency": "usd",
		"publicCheckoutAvailable": false,
	}
	if c.enabled {
		response["shoppingCents"] = c.shoppingCents
		response["deliveryCents"] = c.deliveryCents
		response["totalCents"] = c.shoppingCents + c.deliveryCents
	}
	writeJSON(w, http.StatusOK, response)
}
