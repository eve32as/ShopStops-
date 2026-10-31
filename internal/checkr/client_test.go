package checkr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientCreatesCandidateInvitationAndFetchesReport(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		username, password, ok := r.BasicAuth()
		if !ok || username != "secret-test-key" || password != "" {
			t.Errorf("unexpected Checkr basic auth: %q, %q, %v", username, password, ok)
		}
		switch r.URL.Path {
		case "/v1/candidates":
			var body struct {
				FirstName    string `json:"first_name"`
				LastName     string `json:"last_name"`
				Email        string `json:"email"`
				WorkLocation []struct {
					City    string `json:"city"`
					State   string `json:"state"`
					Country string `json:"country"`
				} `json:"work_locations"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.FirstName != "Test" || body.LastName != "Driver" ||
				body.Email != "driver@example.test" || len(body.WorkLocation) != 1 ||
				body.WorkLocation[0].City != "Washington" ||
				body.WorkLocation[0].State != "DC" || body.WorkLocation[0].Country != "US" {
				t.Errorf("candidate request fields = %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "candidate-1"})
		case "/v1/invitations":
			var body struct {
				CandidateID   string `json:"candidate_id"`
				Package       string `json:"package"`
				WorkLocations []struct {
					City    string `json:"city"`
					State   string `json:"state"`
					Country string `json:"country"`
				} `json:"work_locations"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.CandidateID != "candidate-1" || body.Package != "delivery_driver" ||
				len(body.WorkLocations) != 1 || body.WorkLocations[0].City != "Washington" ||
				body.WorkLocations[0].State != "DC" || body.WorkLocations[0].Country != "US" {
				t.Errorf("invitation request fields = %#v", body)
			}
			_ = json.NewEncoder(w).Encode(Invitation{
				ID: "invitation-1", Status: "pending", ReportID: "report-1",
			})
		case "/v1/invitations/invitation-1":
			_ = json.NewEncoder(w).Encode(Invitation{
				ID: "invitation-1", Status: "completed", ReportID: "report-1",
			})
		case "/v1/reports/report-1":
			_ = json.NewEncoder(w).Encode(Report{
				ID: "report-1", Status: "complete", Result: "clear",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient("secret-test-key", "delivery_driver", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	candidateID, err := client.CreateCandidate(context.Background(), Candidate{
		FirstName: "Test", LastName: "Driver", Email: "driver@example.test",
		WorkLocation: WorkLocation{
			City: "Washington", Region: "DC", Country: "US",
		},
	})
	if err != nil || candidateID != "candidate-1" {
		t.Fatalf("CreateCandidate = %q, %v", candidateID, err)
	}
	invitation, err := client.CreateInvitation(context.Background(), candidateID, WorkLocation{
		City: "Washington", Region: "DC", Country: "US",
	})
	if err != nil || invitation.ID != "invitation-1" {
		t.Fatalf("CreateInvitation = %#v, %v", invitation, err)
	}
	invitation, err = client.GetInvitation(context.Background(), invitation.ID)
	if err != nil || invitation.ReportID != "report-1" {
		t.Fatalf("GetInvitation = %#v, %v", invitation, err)
	}
	report, err := client.GetReport(context.Background(), invitation.ReportID)
	if err != nil || report.Status != "complete" || report.Result != "clear" {
		t.Fatalf("GetReport = %#v, %v", report, err)
	}
	want := []string{
		"POST /v1/candidates", "POST /v1/invitations",
		"GET /v1/invitations/invitation-1", "GET /v1/reports/report-1",
	}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("request paths = %v, want %v", paths, want)
	}
}

func TestClientDoesNotFollowRedirectOrLeakProviderResponse(t *testing.T) {
	const secretBody = "sensitive provider response"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(secretBody))
	}))
	defer server.Close()
	client, err := NewClient("secret-test-key", "package", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateCandidate(context.Background(), Candidate{})
	if err == nil || strings.Contains(err.Error(), secretBody) || strings.Contains(err.Error(), "secret-test-key") {
		t.Fatalf("provider error leaked sensitive data: %v", err)
	}
}

func TestClientConfigurationRequiresHTTPSOutsideLoopback(t *testing.T) {
	for _, baseURL := range []string{
		"http://checkr.example", "******checkr.example",
		"https://checkr.example/path", "https://checkr.example?debug=true",
	} {
		if _, err := NewClient("key", "package", baseURL); err == nil {
			t.Errorf("NewClient accepted unsafe URL %q", baseURL)
		}
	}
	if _, err := NewClient("key", "package", "http://127.0.0.1:8080"); err != nil {
		t.Fatalf("loopback test URL rejected: %v", err)
	}
	if _, err := NewClient("key", "package", "https://checkr.example"); err == nil {
		t.Fatal("NewClient accepted a non-Checkr HTTPS host")
	}
}
