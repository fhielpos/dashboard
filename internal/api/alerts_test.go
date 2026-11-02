package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/franco/dashboard/internal/alerts"
)

func TestHandleAlerts_ReturnsBufferedAlerts(t *testing.T) {
	buf := alerts.NewBuffer(10, time.Hour)
	buf.Add(alerts.Alert{Title: "Review my PR", URL: "http://x/pull/1", Repo: "o/r", Reason: "review_requested"})
	s := &Server{alerts: buf}

	req := httptest.NewRequest("GET", "/api/alerts?since=0", nil)
	w := httptest.NewRecorder()
	s.handleAlerts(w, req)

	var resp struct {
		Alerts []struct {
			Title string `json:"title"`
			Body  string `json:"body"`
			URL   string `json:"url"`
		} `json:"alerts"`
		Cursor int64 `json:"cursor"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Cursor != 1 {
		t.Errorf("cursor = %d, want 1", resp.Cursor)
	}
	if len(resp.Alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(resp.Alerts))
	}
	if resp.Alerts[0].Body != "Review my PR" || resp.Alerts[0].URL != "http://x/pull/1" {
		t.Errorf("alert payload wrong: %+v", resp.Alerts[0])
	}
	if resp.Alerts[0].Title == "" {
		t.Errorf("title (headline) should be set")
	}
}

func TestHandleAlerts_NilBufferIsEmpty(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/api/alerts", nil)
	w := httptest.NewRecorder()
	s.handleAlerts(w, req)

	var resp struct {
		Alerts []any `json:"alerts"`
		Cursor int64 `json:"cursor"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Alerts) != 0 {
		t.Errorf("nil buffer should return no alerts")
	}
}
