package gateway

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Keep the audited enabled/blocked policy synchronized with the exact OpenAPI
// operation set. A new operation must be explicitly reviewed, never proxied by
// a catch-all. This intentionally reads the contract's current indentation.
func TestOperationPolicyMatchesOpenAPI(t *testing.T) {
	f, err := os.Open("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pathPattern := regexp.MustCompile(`^  (/[^:]+):$`)
	methodPattern := regexp.MustCompile(`^    (get|post|patch|put|delete|head|options):$`)
	parameterPattern := regexp.MustCompile(`\{[^}]+\}`)
	contract := make(map[string]bool)
	path := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if match := pathPattern.FindStringSubmatch(line); match != nil {
			path = parameterPattern.ReplaceAllString(match[1], "{id}")
		} else if match := methodPattern.FindStringSubmatch(line); match != nil && path != "" {
			contract[strings.ToUpper(match[1])+" "+path] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(contract) != len(operations) {
		t.Fatalf("contract has %d operations; gateway has %d explicit policies", len(contract), len(operations))
	}
	for _, op := range operations {
		key := op.method + " " + op.path
		if !contract[key] {
			t.Fatalf("gateway operation not in contract: %s", key)
		}
		delete(contract, key)
		switch op.role {
		case "", "customer", "merchant", "admin", "blocked":
		default:
			t.Fatalf("unreviewed route role: %s", op.role)
		}
		path := strings.ReplaceAll(op.path, "{id}", testID)
		if _, ok := route(op.method, path); !ok {
			t.Fatalf("explicit operation does not match: %s", key)
		}
	}
	if len(contract) != 0 {
		t.Fatal("OpenAPI operation lacks an explicit gateway policy")
	}
}
