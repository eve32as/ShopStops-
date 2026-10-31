package checkr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 1 << 20

type Client struct {
	apiKey    string
	packageID string
	baseURL   string
	http      *http.Client
}

type Candidate struct {
	FirstName    string
	LastName     string
	Email        string
	WorkLocation WorkLocation
}

type WorkLocation struct {
	City    string
	Region  string
	Country string
}

type Invitation struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	ReportID string `json:"report_id"`
}

type Report struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Result string `json:"result"`
}

type APIError struct {
	StatusCode int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Checkr API returned HTTP %d", e.StatusCode)
}

func NewClient(apiKey, packageID, baseURL string) (*Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	packageID = strings.TrimSpace(packageID)
	if apiKey == "" || packageID == "" {
		return nil, errors.New("Checkr API key and package are required")
	}
	if baseURL == "" {
		baseURL = "https://api.checkr-staging.com"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		strings.TrimRight(parsed.Path, "/") != "" || strings.TrimSpace(baseURL) != baseURL {
		return nil, errors.New("Checkr API base URL must be an absolute HTTP(S) origin")
	}
	if !isLoopback(parsed.Hostname()) &&
		(parsed.Scheme != "https" ||
			(parsed.Hostname() != "api.checkr.com" &&
				parsed.Hostname() != "api.checkr-staging.com") ||
			(parsed.Port() != "" && parsed.Port() != "443")) {
		return nil, errors.New("Checkr API URL must use an official HTTPS endpoint")
	}
	return &Client{
		apiKey: apiKey, packageID: packageID, baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(
			_ *http.Request, _ []*http.Request,
		) error {
			return http.ErrUseLastResponse
		}, Transport: &http.Transport{
			MaxConnsPerHost:       20,
			MaxIdleConns:          20,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		}},
	}, nil
}

func (c *Client) PackageID() string {
	return c.packageID
}

func (c *Client) CreateCandidate(ctx context.Context, candidate Candidate) (string, error) {
	var response struct {
		ID string `json:"id"`
	}
	err := c.request(ctx, http.MethodPost, "/v1/candidates", map[string]any{
		"first_name": candidate.FirstName,
		"last_name":  candidate.LastName,
		"email":      candidate.Email,
		"work_locations": []map[string]string{{
			"city":    candidate.WorkLocation.City,
			"state":   candidate.WorkLocation.Region,
			"country": candidate.WorkLocation.Country,
		}},
	}, &response)
	if err != nil {
		return "", err
	}
	if response.ID == "" {
		return "", errors.New("Checkr returned an invalid candidate response")
	}
	return response.ID, nil
}

func (c *Client) CreateInvitation(
	ctx context.Context, candidateID string, location WorkLocation,
) (Invitation, error) {
	var response Invitation
	err := c.request(ctx, http.MethodPost, "/v1/invitations", map[string]any{
		"candidate_id": candidateID,
		"package":      c.packageID,
		"work_locations": []map[string]string{{
			"city": location.City, "state": location.Region, "country": location.Country,
		}},
	}, &response)
	if err != nil {
		return Invitation{}, err
	}
	if response.ID == "" {
		return Invitation{}, errors.New("Checkr returned an invalid invitation response")
	}
	return response, nil
}

func (c *Client) GetInvitation(ctx context.Context, invitationID string) (Invitation, error) {
	var response Invitation
	err := c.request(
		ctx, http.MethodGet, "/v1/invitations/"+url.PathEscape(invitationID), nil, &response,
	)
	if err != nil {
		return Invitation{}, err
	}
	return response, nil
}

func (c *Client) GetReport(ctx context.Context, reportID string) (Report, error) {
	var response Report
	err := c.request(ctx, http.MethodGet, "/v1/reports/"+url.PathEscape(reportID), nil, &response)
	if err != nil {
		return Report{}, err
	}
	return response, nil
}

func (c *Client) request(
	ctx context.Context, method, path string, body any, result any,
) error {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return errors.New("could not encode Checkr request")
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(
		ctx, method, c.baseURL+path, requestBody,
	)
	if err != nil {
		return errors.New("could not create Checkr request")
	}
	request.SetBasicAuth(c.apiKey, "")
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return errors.New("Checkr request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
		return &APIError{StatusCode: response.StatusCode}
	}
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) > maxResponseBytes {
		return errors.New("invalid Checkr response")
	}
	if err := json.Unmarshal(data, result); err != nil {
		return errors.New("invalid Checkr response")
	}
	return nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
