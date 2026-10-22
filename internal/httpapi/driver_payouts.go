package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"

	"github.com/stripe/stripe-go/v82"
)

type DriverConnectStatus struct {
	AccountID        string `json:"-"`
	DetailsSubmitted bool   `json:"detailsSubmitted"`
	PayoutsEnabled   bool   `json:"payoutsEnabled"`
}

type DriverPayoutGateway interface {
	CreateDriverConnectAccount(context.Context, string, string) (string, error)
	CreateDriverConnectLink(context.Context, string) (string, error)
	RetrieveDriverConnectStatus(context.Context, string) (DriverConnectStatus, error)
	CreateDriverTransfer(context.Context, string, int64, string, string) (string, error)
	CreateDriverPayout(context.Context, string, int64, string, string) (string, string, error)
	RetrieveDriverPayout(context.Context, string, string) (string, error)
}

func (g *StripeGateway) CreateDriverConnectAccount(
	ctx context.Context, country, idempotencyKey string,
) (string, error) {
	params := &stripe.AccountCreateParams{
		Country: stripe.String(country),
		Type:    stripe.String("express"),
		Capabilities: &stripe.AccountCreateCapabilitiesParams{
			Transfers: &stripe.AccountCreateCapabilitiesTransfersParams{
				Requested: stripe.Bool(true),
			},
		},
	}
	params.SetIdempotencyKey(idempotencyKey)
	account, err := g.client.V1Accounts.Create(ctx, params)
	if err != nil {
		return "", err
	}
	return account.ID, nil
}

func (g *StripeGateway) CreateDriverConnectLink(
	ctx context.Context, accountID string,
) (string, error) {
	if !validConnectRedirectURL(g.connectReturnURL) ||
		!validConnectRedirectURL(g.connectRefreshURL) {
		return "", errors.New("Stripe Connect return and refresh URLs are not configured")
	}
	params := &stripe.AccountLinkCreateParams{
		Account:    stripe.String(accountID),
		RefreshURL: stripe.String(g.connectRefreshURL),
		ReturnURL:  stripe.String(g.connectReturnURL),
		Type:       stripe.String("account_onboarding"),
	}
	link, err := g.client.V1AccountLinks.Create(ctx, params)
	if err != nil {
		return "", err
	}
	return link.URL, nil
}

func (g *StripeGateway) RetrieveDriverConnectStatus(
	ctx context.Context, accountID string,
) (DriverConnectStatus, error) {
	account, err := g.client.V1Accounts.GetByID(
		ctx, accountID, &stripe.AccountRetrieveParams{},
	)
	if err != nil {
		return DriverConnectStatus{}, err
	}
	return DriverConnectStatus{
		AccountID: account.ID, DetailsSubmitted: account.DetailsSubmitted,
		PayoutsEnabled: account.PayoutsEnabled,
	}, nil
}

func (g *StripeGateway) CreateDriverTransfer(
	ctx context.Context, accountID string, amount int64, requestID, idempotencyKey string,
) (string, error) {
	params := &stripe.TransferCreateParams{
		Amount:      stripe.Int64(amount),
		Currency:    stripe.String("usd"),
		Destination: stripe.String(accountID),
		TransferGroup: stripe.String(
			"shopstops_driver_cashout_" + requestID,
		),
		Metadata: map[string]string{"shopstops_driver_cashout_id": requestID},
	}
	params.SetIdempotencyKey(idempotencyKey)
	transfer, err := g.client.V1Transfers.Create(ctx, params)
	if err != nil {
		return "", err
	}
	return transfer.ID, nil
}

func (g *StripeGateway) CreateDriverPayout(
	ctx context.Context, accountID string, amount int64, requestID, idempotencyKey string,
) (string, string, error) {
	params := &stripe.PayoutCreateParams{
		Amount:   stripe.Int64(amount),
		Currency: stripe.String("usd"),
		Metadata: map[string]string{"shopstops_driver_cashout_id": requestID},
	}
	params.SetStripeAccount(accountID)
	params.SetIdempotencyKey(idempotencyKey)
	payout, err := g.client.V1Payouts.Create(ctx, params)
	if err != nil {
		return "", "", err
	}
	return payout.ID, string(payout.Status), nil
}

func (g *StripeGateway) RetrieveDriverPayout(
	ctx context.Context, accountID, payoutID string,
) (string, error) {
	params := &stripe.PayoutRetrieveParams{}
	params.SetStripeAccount(accountID)
	payout, err := g.client.V1Payouts.Retrieve(ctx, payoutID, params)
	if err != nil {
		return "", err
	}
	return string(payout.Status), nil
}

func validConnectRedirectURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.Fragment != "" || parsed.Scheme != "https" &&
		!(parsed.Scheme == "http" &&
			(parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1")) {
		return false
	}
	return strings.TrimSpace(raw) == raw
}

func driverAccountIdempotencyKey(driverRef string) string {
	sum := sha256.Sum256([]byte(driverRef))
	return "shopstops-driver-account-" + hex.EncodeToString(sum[:])
}
