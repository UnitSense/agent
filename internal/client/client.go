package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Client struct {
	baseURL             string
	bearer              string
	http                *http.Client
	RetryBackoffSeconds []int
}

func New(baseURL, bearer string) *Client {
	return &Client{
		baseURL:             strings.TrimRight(baseURL, "/"),
		bearer:              bearer,
		http:                &http.Client{Timeout: 30 * time.Second},
		RetryBackoffSeconds: []int{1, 2, 4},
	}
}

type ValidateResponse struct {
	Valid          bool   `json:"valid"`
	TokenType      string `json:"token_type"`
	TenantSlug     string `json:"tenant_slug"`
	DeveloperEmail string `json:"developer_email,omitempty"`
	MachineLabel   string `json:"machine_label,omitempty"`
}

type RegisterRequest struct {
	DeveloperEmail string    `json:"developer_email"`
	MachineID      uuid.UUID `json:"machine_id"`
	MachineLabel   string    `json:"machine_label,omitempty"`
	AgentVersion   string    `json:"agent_version"`
}

type RegisterResponse struct {
	DeviceToken string `json:"device_token"`
	DeveloperID string `json:"developer_id"`
	TenantSlug  string `json:"tenant_slug"`
}

type EventsRequest struct {
	RequestID     uuid.UUID        `json:"request_id"`
	AgentVersion  string           `json:"agent_version"`
	ParserVersion string           `json:"parser_version"`
	Provider      string           `json:"provider"`
	Events        []map[string]any `json:"events"`
}

type EventsResponse struct {
	AcceptedCount        int    `json:"accepted_count"`
	RunID                string `json:"run_id"`
	DeveloperID          string `json:"developer_id"`
	ProviderConnectionID string `json:"provider_connection_id"`
	Duplicate            bool   `json:"duplicate"`
}

type SessionsRequest struct {
	RequestID        uuid.UUID        `json:"request_id"`
	AgentVersion     string           `json:"agent_version"`
	ParserVersion    string           `json:"parser_version"`
	Provider         string           `json:"provider"`
	SessionSummaries []map[string]any `json:"session_summaries"`
}

type SessionsResponse struct {
	AcceptedCount int    `json:"accepted_count"`
	RunID         string `json:"run_id"`
	Duplicate     bool   `json:"duplicate"`
}

// RateLimitsRequest posts the latest subscription-quota snapshot(s) for a
// provider. Snapshots are point-in-time gauges; the server upserts the latest
// per developer+provider. Developer attribution is resolved server-side from
// the device token (same as events/sessions).
type RateLimitsRequest struct {
	RequestID     uuid.UUID        `json:"request_id"`
	AgentVersion  string           `json:"agent_version"`
	ParserVersion string           `json:"parser_version"`
	Provider      string           `json:"provider"`
	Snapshots     []map[string]any `json:"snapshots"`
}

type RateLimitsResponse struct {
	AcceptedCount int    `json:"accepted_count"`
	RunID         string `json:"run_id"`
}

func (c *Client) Validate() (*ValidateResponse, error) {
	var resp ValidateResponse
	if err := c.do("POST", "/api/v1/agent/validate", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Register(req RegisterRequest) (*RegisterResponse, error) {
	var resp RegisterResponse
	if err := c.do("POST", "/api/v1/agent/register", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) PostEvents(req EventsRequest) (*EventsResponse, error) {
	var resp EventsResponse
	if err := c.do("POST", "/api/v1/agent/events", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) PostSessions(req SessionsRequest) (*SessionsResponse, error) {
	var resp SessionsResponse
	if err := c.do("POST", "/api/v1/agent/sessions", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) PostRateLimits(req RateLimitsRequest) (*RateLimitsResponse, error) {
	var resp RateLimitsResponse
	if err := c.do("POST", "/api/v1/agent/rate-limits", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) do(method, path string, body, out any) error {
	if err := c.checkScheme(); err != nil {
		return err
	}
	u := c.baseURL + path

	var lastErr error
	attempts := len(c.RetryBackoffSeconds) + 1
	for i := 0; i < attempts; i++ {
		var bodyReader io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return err
			}
			bodyReader = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, u, bodyReader)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.bearer)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "unitsense-agent")

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
		} else {
			if resp.StatusCode >= 500 {
				resp.Body.Close()
				lastErr = fmt.Errorf("server %d", resp.StatusCode)
			} else if resp.StatusCode >= 400 {
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
			} else {
				defer resp.Body.Close()
				if out == nil {
					return nil
				}
				return json.NewDecoder(resp.Body).Decode(out)
			}
		}
		if i < attempts-1 {
			time.Sleep(time.Duration(c.RetryBackoffSeconds[i]) * time.Second)
		}
	}
	return errors.New("exhausted retries: " + lastErr.Error())
}

func (c *Client) checkScheme() error {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return err
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && (u.Hostname() == "localhost" || strings.HasPrefix(u.Hostname(), "127.") || u.Hostname() == "::1") {
		return nil
	}
	return fmt.Errorf("base URL must be HTTPS (got %s)", u.Scheme)
}
