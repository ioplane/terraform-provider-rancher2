package rancher2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestFeatureUpdateRecoversAfterReadinessFailures(t *testing.T) {
	for _, tc := range []struct {
		name         string
		failures     int
		updateStatus int
		wantError    bool
	}{
		{name: "ready_success"},
		{name: "recovery_after_21_failures", failures: 21},
		{name: "update_502_recovers", updateStatus: http.StatusBadGateway},
		{name: "update_503_recovers", updateStatus: http.StatusServiceUnavailable},
		{name: "update_403_fails", updateStatus: http.StatusForbidden, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const featureID = "synthetic-feature"
			var mu sync.Mutex
			var updates, initialReads, readinessFailures, readinessSuccesses, postUpdateReads int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method == http.MethodGet && r.URL.Path == "/ping" {
					if updates > 0 {
						if readinessFailures < tc.failures {
							readinessFailures++
							http.Error(w, "synthetic readiness failure", http.StatusServiceUnavailable)
							return
						}
						readinessSuccesses++
					}
					if _, err := w.Write([]byte("pong")); err != nil {
						t.Errorf("write readiness response: %v", err)
					}
					return
				}
				baseURL := "http://" + r.Host
				w.Header().Set("Content-Type", "application/json")
				var response interface{}
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v3":
					w.Header().Set("X-API-Schemas", baseURL+"/v3")
					response = map[string]interface{}{"data": []interface{}{
						map[string]interface{}{"id": "setting", "links": map[string]string{"collection": baseURL + "/v3/settings"}, "resourceMethods": []string{"GET"}},
						map[string]interface{}{"id": "feature", "links": map[string]string{"collection": baseURL + "/v3/features"}, "resourceMethods": []string{"GET", "PUT"}},
					}}
				case r.Method == http.MethodGet && r.URL.Path == "/v3/settings/k8s-version":
					response = map[string]string{"id": "k8s-version", "value": ""}
				case r.Method == http.MethodGet && r.URL.Path == "/v3/features/"+featureID:
					labels := map[string]string{"fixture": "initial"}
					if updates == 0 {
						initialReads++
					} else {
						if readinessSuccesses != 1 {
							t.Errorf("feature read before readiness recovery: successes=%d", readinessSuccesses)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						postUpdateReads++
						if postUpdateReads == 2 {
							labels["fixture"] = "refreshed"
						}
					}
					response = map[string]interface{}{"id": featureID, "type": "feature", "name": featureID, "value": updates > 0, "labels": labels, "links": map[string]string{"self": baseURL + "/v3/features/" + featureID}}
				case r.Method == http.MethodPut && r.URL.Path == "/v3/features/"+featureID && updates == 0 && initialReads == 1:
					var update struct {
						Value bool `json:"value"`
					}
					if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
						t.Errorf("decode feature update: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if !update.Value {
						t.Error("feature update did not request value=true")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					updates++
					if tc.updateStatus != 0 {
						http.Error(w, "synthetic update failure", tc.updateStatus)
						return
					}
					response = map[string]interface{}{"id": featureID, "type": "feature", "name": featureID, "value": true, "links": map[string]string{"self": baseURL + "/v3/features/" + featureID}}
				default:
					t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Errorf("encode response: %v", err)
				}
			}))
			defer srv.Close()

			d := schema.TestResourceDataRaw(t, resourceRancher2Feature().Schema, map[string]interface{}{
				"name": featureID, "value": true,
			})
			d.SetId(featureID)
			config := &Config{URL: srv.URL, TokenKey: "synthetic-token", Timeout: 100 * time.Millisecond, K8SDefaultVersion: rancher2ClusterRKEK8SDefaultVersion}
			started := time.Now()
			err := resourceRancher2FeatureUpdate(d, config)
			mu.Lock()
			defer mu.Unlock()
			t.Logf("Update elapsed=%s updates=%d initial_reads=%d readiness_failures=%d readiness_successes=%d post_update_reads=%d error=%v", time.Since(started), updates, initialReads, readinessFailures, readinessSuccesses, postUpdateReads, err)
			if tc.wantError {
				if err == nil {
					t.Error("Update must return the fatal update error")
				}
				if updates != 1 || initialReads != 1 || readinessFailures != 0 || readinessSuccesses != 0 || postUpdateReads != 0 {
					t.Errorf("fatal Update continued to readiness or Read: updates=%d initial_reads=%d failures=%d successes=%d post_update_reads=%d", updates, initialReads, readinessFailures, readinessSuccesses, postUpdateReads)
				}
				if d.Id() != featureID {
					t.Errorf("fatal Update ID = %q, want %q", d.Id(), featureID)
				}
				if state := d.State(); state == nil || state.ID != featureID {
					t.Errorf("fatal Update state = %#v, want state ID %s", state, featureID)
				}
				return
			}
			if err != nil {
				t.Errorf("Update must recover after %d readiness failures: %v", tc.failures, err)
			}
			if updates != 1 || initialReads != 1 || readinessFailures != tc.failures || readinessSuccesses != 1 || postUpdateReads != 2 {
				t.Errorf("Update did not reach recovery and final Read: updates=%d initial_reads=%d failures=%d successes=%d post_update_reads=%d", updates, initialReads, readinessFailures, readinessSuccesses, postUpdateReads)
			}
			if d.Id() != featureID {
				t.Errorf("Update ID = %q, want %q", d.Id(), featureID)
			}
			if state := d.State(); state == nil || state.ID != featureID {
				t.Errorf("Update state = %#v, want state ID %s", state, featureID)
			}
			if labels, ok := d.Get("labels").(map[string]interface{}); !ok || labels["fixture"] != "refreshed" {
				t.Errorf("final Read labels = %v, want fixture=refreshed", labels)
			}
		})
	}
}
