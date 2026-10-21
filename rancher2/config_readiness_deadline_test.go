package rancher2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestRancherReadinessDeadline(t *testing.T) {
	for _, stage := range []string{"headers", "body"} {
		t.Run(stage, func(t *testing.T) {
			entered := make(chan struct{})
			canceled := make(chan struct{})
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/ping" {
					t.Errorf("unexpected readiness request: %s %s", r.Method, r.URL.Path)
				}
				if stage == "body" {
					w.Header().Set("Content-Length", "4")
					if _, err := io.WriteString(w, "po"); err != nil {
						t.Errorf("write partial body: %v", err)
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Errorf("flush partial body: %v", err)
					}
				}
				close(entered)
				select {
				case <-r.Context().Done():
					close(canceled)
				case <-release:
				}
			}))
			stopped := make(chan struct{})
			result := make(chan error, 1)
			t.Cleanup(func() {
				close(release)
				srv.CloseClientConnections()
				srv.Close()
				select {
				case <-stopped:
				case <-time.After(2 * time.Second):
					t.Error("readiness worker did not stop during fixture cleanup")
				}
			})
			go func() {
				defer close(stopped)
				c := &Config{URL: srv.URL, Timeout: 250 * time.Millisecond}
				result <- c.isRancherReady()
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("readiness request did not reach handler")
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("readiness error = %v; want wrapped context.DeadlineExceeded", err)
				}
				if stage == "headers" {
					var probeErr *url.Error
					if !errors.As(err, &probeErr) {
						t.Errorf("readiness error = %v; want retained *url.Error", err)
					}
				}
			case <-time.After(2 * time.Second):
				t.Error("readiness exceeded bounded request deadline while response was blocked")
			}
			select {
			case <-canceled:
				t.Log("handler observed request-context cancellation before fixture release")
			case <-time.After(time.Second):
				t.Error("handler did not observe request-context cancellation before fixture release")
			}
		})
	}

	for _, tc := range []struct {
		name    string
		timeout time.Duration
		body    string
		wantErr bool
	}{
		{name: "zero_first_probe", body: "pong"},
		{name: "negative_first_probe", timeout: -time.Second, body: "pong"},
		{name: "pong", timeout: time.Second, body: "pong"},
		{name: "unexpected_reply", timeout: 250 * time.Millisecond, body: "not ready", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan string, 2)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.Method + " " + r.URL.Path
				if _, err := io.WriteString(w, tc.body); err != nil {
					t.Errorf("write readiness reply: %v", err)
				}
			}))
			defer srv.Close()
			c := &Config{URL: srv.URL, Timeout: tc.timeout}
			err := c.isRancherReady()
			if tc.wantErr {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("readiness error = %v; want wrapped context.DeadlineExceeded", err)
				}
			} else if err != nil {
				t.Errorf("first complete pong probe failed: %v", err)
			}
			select {
			case request := <-requests:
				if request != "GET /ping" {
					t.Errorf("readiness request = %q; want GET /ping", request)
				}
			default:
				t.Error("readiness did not send an actual request")
			}
			if len(requests) != 0 {
				t.Error("unexpected additional readiness requests")
			}
		})
	}
}
