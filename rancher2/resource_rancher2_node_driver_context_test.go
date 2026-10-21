package rancher2

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestNodeDriverContext(t *testing.T) {
	for _, operation := range []string{"create", "read", "update", "delete", "import"} {
		phases := []string{"success", "pre_canceled", "request_canceled"}
		switch operation {
		case "create":
			phases = append(phases, "readiness_canceled", "missing_response_id", "schema_canceled")
		case "read":
			phases = append(phases, "lookup_identity")
		case "update":
			phases = append(phases, "lookup_identity", "response_identity", "snapshot")
		case "delete":
			phases = append(phases, "readiness_canceled", "builtin")
		}
		for _, phase := range phases {
			t.Run(operation+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				block := newSettingContextBlock()
				var written atomic.Bool
				var config *Config
				f := newSettingContextFixture(t, true, func(w http.ResponseWriter, r *http.Request) bool {
					if r.URL.Path == "/v3" {
						if phase == "schema_canceled" {
							cancel()
							block.serve(w, r, false, http.StatusOK)
							return true
						}
						base := "http://" + r.Host + "/v3"
						w.Header().Set("X-API-Schemas", base)
						if err := json.NewEncoder(w).Encode(map[string]any{"data": []any{
							map[string]any{"id": "setting", "links": map[string]string{"collection": base + "/settings"}, "resourceMethods": []string{"GET"}},
							map[string]any{"id": "nodeDriver", "links": map[string]string{"collection": base + "/nodeDrivers"}, "resourceMethods": []string{"GET", "PUT", "DELETE"}, "collectionMethods": []string{"POST"}},
						}}); err != nil {
							t.Errorf("schema response: %v", err)
						}
						return true
					}
					if !strings.HasPrefix(r.URL.Path, "/v3/nodeDrivers") {
						return false
					}
					if r.Header.Get("Authorization") != "Bearer old-token" {
						t.Errorf("operation changed its client snapshot: %q", r.Header.Get("Authorization"))
					}
					mutation := r.Method != http.MethodGet
					if phase == "request_canceled" && (mutation || operation == "read" || operation == "import") || phase == "readiness_canceled" && !mutation && written.Load() {
						cancel()
						block.serve(w, r, false, http.StatusOK)
						return true
					}
					if mutation {
						want := map[string]string{"create": http.MethodPost, "update": http.MethodPut, "delete": http.MethodDelete}[operation]
						if r.Method != want || r.URL.Path != "/v3/nodeDrivers" && r.URL.Path != "/v3/nodeDrivers/pilot" {
							t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
						}
						if r.Method != http.MethodDelete {
							var payload map[string]any
							if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
								t.Errorf("mutation payload: %v", err)
							}
							builtin, present := payload["builtin"]
							if payload["name"] != "pilot" || payload["url"] != "https://driver.invalid/pilot" || payload["active"] != true || present && builtin != false || operation == "update" && !present {
								t.Errorf("changed driver payload: %v", payload)
							}
						}
						written.Store(true)
						if phase == "snapshot" {
							if err := config.UpdateToken("new-token"); err != nil {
								t.Errorf("token invalidation: %v", err)
							}
						}
						if r.Method == http.MethodDelete {
							w.WriteHeader(http.StatusNoContent)
							return true
						}
					} else if operation == "delete" && written.Load() {
						http.Error(w, "missing", http.StatusNotFound)
						return true
					}
					id := "pilot"
					if phase == "lookup_identity" || phase == "response_identity" && mutation {
						id = "foreign"
					} else if phase == "missing_response_id" && mutation {
						id = ""
					}
					if err := json.NewEncoder(w).Encode(map[string]any{"id": id, "name": "pilot", "url": "https://driver.invalid/pilot", "active": true, "builtin": phase == "builtin", "state": "active", "links": map[string]string{"self": "http://" + r.Host + "/v3/nodeDrivers/pilot"}}); err != nil {
						t.Errorf("driver response: %v", err)
					}
					return true
				})
				t.Cleanup(block.unblock)
				config = f.config()
				d := schema.TestResourceDataRaw(t, nodeDriverFields(), map[string]any{"name": "pilot", "url": "https://driver.invalid/pilot", "active": true, "builtin": false})
				if operation != "create" {
					d.SetId("pilot")
				}
				if phase == "pre_canceled" {
					cancel()
				}
				resource := resourceRancher2NodeDriver()
				var diagnostics diag.Diagnostics
				if operation == "import" {
					_, err := resource.Importer.StateContext(ctx, d, config)
					diagnostics = diag.FromErr(err)
				} else {
					var callback func(context.Context, *schema.ResourceData, interface{}) diag.Diagnostics = resource.ReadContext
					switch operation {
					case "create":
						callback = resource.CreateContext
					case "update":
						callback = resource.UpdateContext
					case "delete":
						callback = resource.DeleteContext
					}
					diagnostics = callback(ctx, d, config)
				}
				success := phase == "success" || phase == "builtin" || phase == "snapshot"
				wantID := "pilot"
				if operation == "delete" && success || operation == "create" && phase != "success" && phase != "readiness_canceled" {
					wantID = ""
				}
				wantMutations := 0
				if (operation == "create" || operation == "update" || operation == "delete") && phase != "pre_canceled" && phase != "lookup_identity" && phase != "schema_canceled" && phase != "builtin" {
					wantMutations = 1
				}
				gets, mutations := f.counts()
				if diagnostics.HasError() == success || d.Id() != wantID || mutations != wantMutations || phase == "pre_canceled" && gets != 0 {
					t.Fatalf("diagnostics=%v ID=%q want=%q GET=%d mutations=%d want=%d", diagnostics, d.Id(), wantID, gets, mutations, wantMutations)
				}
				if wantID != "" && (d.State() == nil || d.State().ID != wantID) {
					t.Fatalf("lost persistent state identity: %v", d.State())
				}
				if strings.HasSuffix(phase, "canceled") && (!diagnostics.HasError() || !strings.Contains(diagnostics[0].Summary, context.Canceled.Error())) {
					t.Fatalf("lost caller cancellation: %v", diagnostics)
				}
			})
		}
	}
}
