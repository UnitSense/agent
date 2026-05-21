package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/validate" || r.Method != "POST" {
			http.Error(w, "wrong path/method", 400)
			return
		}
		if r.Header.Get("Authorization") != "Bearer ust_dev_test" {
			http.Error(w, "no auth", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"valid":       true,
			"token_type":  "device",
			"tenant_slug": "saltest",
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "ust_dev_test")
	res, err := c.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || res.TokenType != "device" || res.TenantSlug != "saltest" {
		t.Fatalf("unexpected: %+v", res)
	}
}

func TestRegister(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_token": "ust_dev_new",
			"developer_id": "00000000-0000-0000-0000-000000000001",
			"tenant_slug":  "saltest",
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "ust_reg_test")
	res, err := c.Register(RegisterRequest{
		DeveloperEmail: "a@b.com",
		MachineID:      uuid.New(),
		AgentVersion:   "0.1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.DeviceToken != "ust_dev_new" {
		t.Fatalf("DeviceToken = %q", res.DeviceToken)
	}
}

func TestPostEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted_count": 1,
			"run_id":         "00000000-0000-0000-0000-000000000002",
			"duplicate":      false,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "ust_dev_test")
	res, err := c.PostEvents(EventsRequest{
		RequestID:     uuid.New(),
		AgentVersion:  "0.1.0",
		ParserVersion: "claude-code-parser-0.3.0",
		Provider:      "agent_claude_code",
		Events:        []map[string]any{{"date": "2026-05-21", "session_count": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.AcceptedCount != 1 {
		t.Fatalf("AcceptedCount = %d", res.AcceptedCount)
	}
}

func TestRetryOn5xx(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			http.Error(w, "transient", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"valid": true, "token_type": "device", "tenant_slug": "x"})
	}))
	defer srv.Close()

	c := New(srv.URL, "ust_dev_test")
	c.RetryBackoffSeconds = []int{0, 0, 0}
	_, err := c.Validate()
	if err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRejectsNonHTTPS(t *testing.T) {
	c := New("http://api.example.com", "ust_dev_test")
	_, err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected HTTPS error, got %v", err)
	}
}
