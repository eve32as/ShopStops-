package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCustodyPINAndDigestAreOrderBound(t *testing.T) {
	secret := "test-only-secret"
	orderID := "00000000-0000-4000-8000-000000000001"
	pin := custodyPIN(secret, orderID)
	if len(pin) != 4 {
		t.Fatalf("custodyPIN() = %q, want four digits", pin)
	}
	for _, digit := range pin {
		if digit < '0' || digit > '9' {
			t.Fatalf("custodyPIN() = %q, want four digits", pin)
		}
	}
	if got := custodyPIN(secret, orderID); got != pin {
		t.Fatalf("custodyPIN() changed across retries: got %q, want %q", got, pin)
	}
	if string(custodyPINDigest(secret, orderID, pin)) ==
		string(custodyPINDigest(secret, orderID, "0000")) {
		t.Fatal("custody PIN digest did not depend on the PIN")
	}
	if string(custodyPINDigest(secret, orderID, pin)) ==
		string(custodyPINDigest(secret, "00000000-0000-4000-8000-000000000002", pin)) {
		t.Fatal("custody PIN digest did not depend on the order")
	}
}

func TestDriverCashoutStatus(t *testing.T) {
	for input, want := range map[string]string{
		"paid": "paid", "failed": "failed", "canceled": "failed",
		"pending": "pending", "in_transit": "pending",
	} {
		if got := driverCashoutStatus(input); got != want {
			t.Errorf("driverCashoutStatus(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDriverConnectRedirectURLValidation(t *testing.T) {
	for _, raw := range []string{
		"https://shopstops.example/connect/return",
		"http://localhost:8080/connect/refresh",
	} {
		if !validConnectRedirectURL(raw) {
			t.Errorf("validConnectRedirectURL(%q) = false", raw)
		}
	}
	for _, raw := range []string{
		"http://shopstops.example/return",
		"******shopstops.example/return",
		"https://shopstops.example/return#fragment",
		"https://shopstops.example/return ",
	} {
		if validConnectRedirectURL(raw) {
			t.Errorf("validConnectRedirectURL(%q) = true", raw)
		}
	}
}

func TestInStoreOrderClaimRequiresShopperRole(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/shopper/in-store/orders/00000000-0000-4000-8000-000000000001/claim",
		strings.NewReader("{}"),
	)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "customer-123")
	request.Header.Set("X-ShopStops-Actor-Role", "customer")
	recorder := httptest.NewRecorder()
	New(&fakeRepository{}, strings.Repeat("t", 32),
		slog.New(slog.NewTextHandler(io.Discard, nil))).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}
