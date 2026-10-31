package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eve32as/ShopStops-/internal/checkr"
)

type screeningTestRepository struct {
	fakeRepository
	screening        DriverScreening
	created          bool
	startCalls       int
	candidateID      string
	invitationID     string
	failureStatus    string
	reviewedDriver   string
	reviewedID       string
	reviewedDecision string
	updateStatus     string
}

func (r *screeningTestRepository) StartDriverScreening(
	_ context.Context, driverRef, packageID, idempotencyKey, actor string,
) (DriverScreening, bool, error) {
	r.startCalls++
	if r.screening.ID != "" {
		return r.screening, false, nil
	}
	r.screening = DriverScreening{
		ID:        "00000000-0000-4000-8000-000000000001",
		PackageID: packageID, Status: "requested", ReviewDecision: "pending",
	}
	r.created = true
	return r.screening, true, nil
}

func (r *screeningTestRepository) SetDriverScreeningCandidate(
	_ context.Context, _ string, candidateID string,
) error {
	r.candidateID = candidateID
	r.screening.Status = "candidate_created"
	return nil
}

func (r *screeningTestRepository) ClaimDriverScreeningInvitation(
	_ context.Context, _ string,
) (string, error) {
	r.screening.Status = "invitation_submitting"
	return r.candidateID, nil
}

func (r *screeningTestRepository) SetDriverScreeningInvitation(
	_ context.Context, _ string, invitationID string,
) error {
	r.invitationID = invitationID
	r.screening.Status = "invited"
	return nil
}

func (r *screeningTestRepository) SetDriverScreeningFailure(
	_ context.Context, _ string, status string,
) error {
	r.failureStatus = status
	r.screening.Status = status
	return nil
}

func (r *screeningTestRepository) LatestDriverScreening(
	_ context.Context, _ string,
) (DriverScreening, string, error) {
	if r.screening.ID == "" {
		return DriverScreening{}, "", ErrNotFound
	}
	return r.screening, r.invitationID, nil
}

func (r *screeningTestRepository) UpdateDriverScreeningProviderStatus(
	_ context.Context, _ string, _ string, status, providerStatus, providerResult string,
) (DriverScreening, error) {
	r.updateStatus = status
	r.screening.Status = status
	r.screening.ProviderStatus = providerStatus
	r.screening.ProviderResult = providerResult
	return r.screening, nil
}

func (r *screeningTestRepository) ReviewDriverScreening(
	_ context.Context, driverRef, screeningID, decision, actor string,
) (DriverScreening, error) {
	r.reviewedDriver, r.reviewedID, r.reviewedDecision = driverRef, screeningID, decision
	r.screening.Status = "complete"
	r.screening.ReviewDecision = decision
	return r.screening, nil
}

type screeningTestProvider struct {
	candidate   checkr.Candidate
	location    checkr.WorkLocation
	candidateID string
	invitation  checkr.Invitation
	report      checkr.Report
	err         error
}

func (p *screeningTestProvider) PackageID() string { return "delivery_driver" }
func (p *screeningTestProvider) CreateCandidate(
	_ context.Context, candidate checkr.Candidate,
) (string, error) {
	p.candidate = candidate
	return "candidate-checkr-1", p.err
}
func (p *screeningTestProvider) CreateInvitation(
	_ context.Context, _ string, location checkr.WorkLocation,
) (checkr.Invitation, error) {
	p.location = location
	return checkr.Invitation{ID: "invitation-checkr-1"}, p.err
}
func (p *screeningTestProvider) GetInvitation(
	_ context.Context, _ string,
) (checkr.Invitation, error) {
	return p.invitation, p.err
}
func (p *screeningTestProvider) GetReport(
	_ context.Context, _ string,
) (checkr.Report, error) {
	return p.report, p.err
}

func TestStartDriverScreeningCreatesHostedInvitationAndIsIdempotent(t *testing.T) {
	repository := &screeningTestRepository{}
	provider := &screeningTestProvider{}
	handler := screeningTestHandler(repository, provider)
	const idempotencyKey = "screening-test-key"
	requestBody := `{
		"firstName":"Test","lastName":"Driver","email":"driver@example.test",
		"workLocation":{"city":"Washington","region":"DC","country":"US"}
	}`
	request := httptest.NewRequest(http.MethodPost, "/admin/drivers/driver-42/screenings",
		strings.NewReader(requestBody))
	request.Header.Set("Idempotency-Key", idempotencyKey)
	addAdminHeaders(request)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body)
	}
	if provider.candidate != (checkr.Candidate{
		FirstName: "Test", LastName: "Driver", Email: "driver@example.test",
	}) {
		t.Errorf("candidate = %#v", provider.candidate)
	}
	if provider.location != (checkr.WorkLocation{
		City: "Washington", Region: "DC", Country: "US",
	}) {
		t.Errorf("work location = %#v", provider.location)
	}
	if strings.Contains(response.Body.String(), "driver@example.test") ||
		strings.Contains(response.Body.String(), "candidate-checkr-1") {
		t.Errorf("response leaked candidate details: %s", response.Body)
	}

	replay := httptest.NewRequest(http.MethodPost, "/admin/drivers/driver-42/screenings",
		strings.NewReader(requestBody))
	replay.Header.Set("Idempotency-Key", idempotencyKey)
	addAdminHeaders(replay)
	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusOK || repository.startCalls != 2 {
		t.Fatalf("replay status/calls = %d/%d", replayResponse.Code, repository.startCalls)
	}
}

func TestRefreshAndReviewDriverScreeningKeepActivationManual(t *testing.T) {
	repository := &screeningTestRepository{
		screening: DriverScreening{
			ID:        "00000000-0000-4000-8000-000000000001",
			PackageID: "delivery_driver", Status: "invited", ReviewDecision: "pending",
		},
		invitationID: "invitation-checkr-1",
	}
	provider := &screeningTestProvider{
		invitation: checkr.Invitation{ID: "invitation-checkr-1", ReportID: "report-checkr-1"},
		report:     checkr.Report{ID: "report-checkr-1", Status: "complete", Result: "consider"},
	}
	handler := screeningTestHandler(repository, provider)
	refresh := httptest.NewRequest(http.MethodPost,
		"/admin/drivers/driver-42/screenings/refresh", nil)
	addAdminHeaders(refresh)
	refreshResponse := httptest.NewRecorder()
	handler.ServeHTTP(refreshResponse, refresh)
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("refresh status = %d: %s", refreshResponse.Code, refreshResponse.Body)
	}
	if repository.screening.Status != "complete" ||
		repository.screening.ProviderResult != "consider" ||
		repository.screening.ReviewDecision != "pending" {
		t.Fatalf("provider result should await admin review: %#v", repository.screening)
	}

	review := httptest.NewRequest(http.MethodPut,
		"/admin/drivers/driver-42/screenings/review",
		strings.NewReader(`{"screeningId":"00000000-0000-4000-8000-000000000001","decision":"approved"}`))
	addAdminHeaders(review)
	reviewResponse := httptest.NewRecorder()
	handler.ServeHTTP(reviewResponse, review)
	if reviewResponse.Code != http.StatusOK || repository.reviewedDecision != "approved" ||
		repository.reviewedDriver != "driver-42" {
		t.Fatalf("review status/recorded decision/driver = %d/%q/%q: %s",
			reviewResponse.Code, repository.reviewedDecision,
			repository.reviewedDriver, reviewResponse.Body)
	}
	if repository.driverProfileStatus == "active" {
		t.Fatal("screening review must not activate a driver")
	}
}

func TestStartDriverScreeningRequiresAdminAndIdempotencyKey(t *testing.T) {
	repository := &screeningTestRepository{}
	handler := screeningTestHandler(repository, &screeningTestProvider{})
	for _, test := range []struct {
		name string
		role string
		key  string
		want int
	}{
		{name: "unauthenticated role", role: "driver", key: "key", want: http.StatusForbidden},
		{name: "missing idempotency key", role: "admin", want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost,
				"/admin/drivers/driver-42/screenings",
				strings.NewReader(`{"firstName":"A","lastName":"B","email":"a@example.test","workLocation":{"city":"DC","region":"DC","country":"US"}}`))
			request.Header.Set("Idempotency-Key", test.key)
			request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
			request.Header.Set("X-ShopStops-Actor", "admin-42")
			request.Header.Set("X-ShopStops-Actor-Role", test.role)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Errorf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

func TestStartDriverScreeningRequiresExplicitReconciliationForAmbiguousProviderError(t *testing.T) {
	repository := &screeningTestRepository{}
	provider := &screeningTestProvider{err: errors.New("private provider details")}
	handler := screeningTestHandler(repository, provider)
	request := httptest.NewRequest(http.MethodPost, "/admin/drivers/driver-42/screenings",
		strings.NewReader(`{"firstName":"Test","lastName":"Driver","email":"driver@example.test","workLocation":{"city":"Washington","region":"DC","country":"US"}}`))
	request.Header.Set("Idempotency-Key", "screening-error-key")
	addAdminHeaders(request)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadGateway || repository.failureStatus != "reconciliation_required" ||
		strings.Contains(response.Body.String(), "private provider details") {
		t.Fatalf("provider failure status/state/body = %d/%q/%s",
			response.Code, repository.failureStatus, response.Body)
	}
}

func screeningTestHandler(repository Repository, provider DriverScreeningProvider) http.Handler {
	return NewWithIntegrations(
		repository, strings.Repeat("t", 32),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil, "", nil, "", provider,
	)
}

func addAdminHeaders(request *http.Request) {
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "admin-42")
	request.Header.Set("X-ShopStops-Actor-Role", "admin")
}
