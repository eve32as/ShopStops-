package gateway

import (
	"regexp"
	"strings"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type operation struct {
	method, path, role string
	limit              int64
}

// Exact api/openapi.yaml operation allowlist. Empty role means public, not
// "any authenticated identity". blocked operations NEVER reach the backend.
var operations = []operation{
	{"GET", "/healthz", "", 0},
	{"POST", "/webhooks/stripe", "", 1024 * 1024},
	{"GET", "/stores/{id}/nearby-merchants", "customer", 0},
	{"GET", "/stores/{id}/catalog", "customer", 0},
	{"POST", "/orders", "customer", 32 * 1024},
	{"GET", "/checkout-pricing/priority", "customer", 0},
	{"GET", "/merchant/store-locations", "merchant", 0},
	{"GET", "/merchant/catalog", "merchant", 0},
	{"POST", "/merchant/catalog", "merchant", 8 * 1024},
	{"PATCH", "/merchant/catalog/{id}", "merchant", 8 * 1024},
	{"GET", "/merchant/subscription", "merchant", 0},
	{"POST", "/merchant/subscription/setup-intent", "merchant", 8 * 1024},
	{"POST", "/merchant/subscription", "merchant", 8 * 1024},
	{"POST", "/admin/checkouts/{id}/refund", "admin", 8 * 1024},
	{"POST", "/admin/finance/scenarios", "admin", 8 * 1024},
	{"GET", "/admin/finance/costs", "admin", 0},
	{"POST", "/admin/finance/costs", "admin", 8 * 1024},
	{"POST", "/admin/shopper/tasks", "admin", 8 * 1024},
	{"GET", "/admin/shopper/tasks", "admin", 0},
	{"POST", "/admin/shopper/tasks/{id}/complete", "admin", 8 * 1024},
	{"GET", "/admin/overview", "admin", 0},
	{"GET", "/admin/checkouts/{id}/refund", "admin", 0},
	{"POST", "/checkouts", "blocked", 0},
	{"POST", "/checkouts/{id}/payment", "blocked", 0},
	{"GET", "/driver/routes/{id}", "blocked", 0},
	{"POST", "/driver/routes/{id}/location", "blocked", 0},
	{"POST", "/driver/routes/{id}/stops/{id}/events", "blocked", 0},
	{"GET", "/driver/bundles/{id}", "blocked", 0},
	{"POST", "/driver/bundles/{id}/stops/{id}/events", "blocked", 0},
	{"POST", "/dispatch/bundles", "blocked", 0},
}

func route(method, path string) (operation, bool) {
	for _, op := range operations {
		if op.method != method {
			continue
		}
		want, got := strings.Split(op.path, "/"), strings.Split(path, "/")
		if len(want) != len(got) {
			continue
		}
		match := true
		for i := range want {
			if want[i] == "{id}" {
				match = match && uuidPattern.MatchString(got[i])
			} else {
				match = match && want[i] == got[i]
			}
		}
		if match {
			return op, true
		}
	}
	return operation{}, false
}
