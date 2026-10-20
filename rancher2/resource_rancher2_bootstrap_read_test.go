package rancher2

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestBootstrapReadPreservesIdentity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bootstrap bool
		ready     bool
	}{
		{name: "login_failure", bootstrap: true},
		{name: "bootstrap_disabled"},
		{name: "successful_refresh", bootstrap: true, ready: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if !tc.bootstrap {
					t.Errorf("bootstrap-disabled Read made an HTTP request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.Method != http.MethodGet {
					t.Errorf("unexpected HTTP method: %s", r.Method)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.URL.Path == "/ping" {
					if !tc.ready {
						http.Error(w, "synthetic readiness failure", http.StatusServiceUnavailable)
						return
					}
					if _, err := w.Write([]byte("pong")); err != nil {
						t.Errorf("write readiness response: %v", err)
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				var response interface{}
				switch r.URL.Path {
				case "/v3":
					baseURL := "http://" + r.Host
					w.Header().Set("X-API-Schemas", baseURL+"/v3")
					response = map[string]interface{}{"data": []interface{}{
						map[string]interface{}{"id": "cluster", "links": map[string]string{"collection": baseURL + "/v3/clusters"}, "resourceMethods": []string{"GET"}},
						map[string]interface{}{"id": "token", "links": map[string]string{"collection": baseURL + "/v3/tokens"}, "resourceMethods": []string{"GET"}},
						map[string]interface{}{"id": "setting", "links": map[string]string{"collection": baseURL + "/v3/settings"}, "resourceMethods": []string{"GET"}},
					}}
				case "/v3/clusters/local":
					response = map[string]interface{}{"id": "local", "conditions": []interface{}{map[string]string{"type": "Updated", "status": "True"}}}
				case "/v3/tokens/synthetic-token":
					response = map[string]interface{}{"id": "synthetic-token", "expired": true}
				case "/v3/settings/k8s-version":
					response = map[string]string{"id": "k8s-version", "value": ""}
				case "/v3/settings/server-url":
					response = map[string]string{"id": "server-url", "value": "https://refreshed.example.invalid"}
				default:
					t.Errorf("unexpected HTTP path: %s", r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Errorf("encode response: %v", err)
				}
			}))
			defer srv.Close()

			d := schema.TestResourceDataRaw(t, resourceRancher2Bootstrap().Schema, map[string]interface{}{
				"token_id": "synthetic-token", "url": "https://previous.example.invalid",
			})
			if tc.ready {
				if err := d.Set("token", "synthetic-token-key"); err != nil {
					t.Fatalf("set token: %v", err)
				}
			}
			d.SetId("user-synthetic")
			config := &Config{URL: srv.URL, Bootstrap: tc.bootstrap, Timeout: time.Second, K8SDefaultVersion: rancher2ClusterRKEK8SDefaultVersion}
			if !tc.ready {
				config.Timeout = 100 * time.Millisecond
			}
			err := resourceRancher2BootstrapRead(d, config)
			if d.Id() != "user-synthetic" {
				t.Errorf("Read ID = %q, want existing user-synthetic", d.Id())
			}
			state := d.State()
			if state == nil || state.ID != "user-synthetic" {
				t.Errorf("Read state = %#v, want existing state ID user-synthetic", state)
			}
			switch {
			case !tc.bootstrap:
				if err == nil || !strings.Contains(err.Error(), "just available on bootstrap mode") {
					t.Errorf("bootstrap-disabled Read error = %v, want mode error", err)
				}
				if requests.Load() != 0 {
					t.Errorf("bootstrap-disabled HTTP requests = %d, want 0", requests.Load())
				}
			case !tc.ready:
				if err == nil {
					t.Error("login failure returned nil error, want wrapped readiness error")
				} else if cause := errors.Unwrap(err); cause == nil || !strings.Contains(cause.Error(), "Rancher is not ready:") {
					t.Errorf("login failure error = %v, want wrapped readiness error", err)
				}
				if requests.Load() == 0 {
					t.Error("login failure did not reach the loopback readiness endpoint")
				}
			default:
				if err != nil {
					t.Fatalf("successful refresh: %v", err)
				}
				if d.Get("url") != "https://refreshed.example.invalid" || d.Get("token_update") != true {
					t.Errorf("refreshed url/token_update = %v/%v, want refreshed URL/true", d.Get("url"), d.Get("token_update"))
				}
			}
		})
	}
}
