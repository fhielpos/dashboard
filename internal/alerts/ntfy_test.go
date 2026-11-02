package alerts

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNtfyChannel_Send(t *testing.T) {
	var gotMethod, gotPath, gotBody, gotTitle, gotClick string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotTitle = r.Header.Get("Title")
		gotClick = r.Header.Get("Click")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := NewNtfyChannel(srv.URL, "mytopic")
	err := ch.Send(context.Background(), Alert{
		Title:  "Please review my PR",
		URL:    "https://github.com/o/r/pull/7",
		Repo:   "o/r",
		Reason: "review_requested",
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/mytopic" {
		t.Errorf("path = %s, want /mytopic", gotPath)
	}
	if gotBody != "Please review my PR" {
		t.Errorf("body = %q, want the alert title", gotBody)
	}
	if gotTitle == "" {
		t.Errorf("Title header should be set")
	}
	if gotClick != "https://github.com/o/r/pull/7" {
		t.Errorf("Click header = %q, want the alert URL", gotClick)
	}
}

func TestNtfyChannel_SendEncodesNonASCIITitle(t *testing.T) {
	var gotTitle string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.Header.Get("Title")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := NewNtfyChannel(srv.URL, "mytopic")
	a := Alert{Title: "x", Repo: "o/r", Reason: "review_requested"}
	if err := ch.Send(context.Background(), a); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < len(gotTitle); i++ {
		if gotTitle[i] >= 0x80 {
			t.Fatalf("Title header must be ASCII, got %q", gotTitle)
		}
	}
	if want := encodeHeader(a.Headline()); gotTitle != want {
		t.Errorf("Title header = %q, want %q", gotTitle, want)
	}
}

func TestEncodeHeader(t *testing.T) {
	if got := encodeHeader("Review requested"); got != "Review requested" {
		t.Errorf("pure-ASCII input should pass through unchanged, got %q", got)
	}
	if got := encodeHeader("Review requested · o/r"); got == "Review requested · o/r" {
		t.Errorf("non-ASCII input should be RFC 2047 encoded, got unchanged %q", got)
	}
}

func TestNtfyChannel_SendErrorsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ch := NewNtfyChannel(srv.URL, "mytopic")
	if err := ch.Send(context.Background(), Alert{Title: "x"}); err == nil {
		t.Errorf("expected error on 500 response")
	}
}
