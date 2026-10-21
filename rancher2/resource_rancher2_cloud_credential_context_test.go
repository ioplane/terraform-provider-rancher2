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

func testCloudCredentialContext(t *testing.T) {
	t.Helper()
	for _, operation := range []string{"create", "read", "update", "delete", "import"} {
		phases := []string{"success", "pre_canceled", "request_canceled"}
		if operation == "create" {
			phases = append(phases, "activation_failed", "activation_canceled", "already_active", "lookup_retry", "readiness_canceled", "missing_id", "snapshot")
		} else if operation != "import" {
			phases = append(phases, "foreign_id")
		}
		for _, phase := range phases {
			t.Run(operation+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				block := newSettingContextBlock()
				var activated, written atomic.Bool
				var actions, writes, lookupFailures atomic.Int32
				var config *Config
				f := newSettingContextFixture(t, true, func(w http.ResponseWriter, r *http.Request) bool {
					base := "http://" + r.Host + "/v3"
					if r.URL.Path == "/v3" {
						w.Header().Set("X-API-Schemas", base)
						if err := json.NewEncoder(w).Encode(map[string]any{"data": []any{
							map[string]any{"id": "setting", "links": map[string]string{"collection": base + "/settings"}, "resourceMethods": []string{"GET"}},
							map[string]any{"id": "nodeDriver", "links": map[string]string{"collection": base + "/nodeDrivers"}, "resourceMethods": []string{"GET"}},
							map[string]any{"id": "cloudCredential", "links": map[string]string{"collection": base + "/cloudCredentials"}, "resourceMethods": []string{"GET", "PUT", "DELETE"}, "collectionMethods": []string{"POST"}},
						}}); err != nil {
							t.Errorf("schema response: %v", err)
						}
						return true
					}
					if !strings.HasPrefix(r.URL.Path, "/v3/nodeDrivers") && !strings.HasPrefix(r.URL.Path, "/v3/cloudCredentials") {
						return false
					}
					if r.Header.Get("Authorization") != "Bearer old-token" {
						t.Errorf("operation changed client snapshot")
					}
					if strings.HasPrefix(r.URL.Path, "/v3/nodeDrivers") {
						if r.Method == http.MethodPost {
							actions.Add(1)
							activated.Store(true)
							switch phase {
							case "activation_failed":
								http.Error(w, "uncertain action result", http.StatusInternalServerError)
							case "activation_canceled":
								cancel()
								block.serve(w, r, false, http.StatusOK)
							default:
								w.WriteHeader(http.StatusNoContent)
							}
							return true
						}
						if phase == "lookup_retry" && activated.Load() && lookupFailures.Add(1) == 1 {
							http.Error(w, "temporary observation failure", http.StatusInternalServerError)
							return true
						}
						state := "inactive"
						if activated.Load() || phase == "already_active" {
							state = "active"
						}
						if err := json.NewEncoder(w).Encode(map[string]any{"id": "nutanix", "state": state, "actions": map[string]string{"activate": base + "/nodeDrivers/nutanix?action=activate"}}); err != nil {
							t.Errorf("driver response: %v", err)
						}
						return true
					}
					mutation := r.Method != http.MethodGet
					if mutation {
						writes.Add(1)
					}
					if phase == "request_canceled" && (mutation || operation == "read" || operation == "import") || phase == "readiness_canceled" && written.Load() && !mutation {
						cancel()
						block.serve(w, r, false, http.StatusOK)
						return true
					}
					if mutation {
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
						http.Error(w, "removed", http.StatusNotFound)
						return true
					}
					id := "pilot"
					if phase == "foreign_id" {
						id = "foreign"
					} else if phase == "missing_id" && mutation {
						id = ""
					}
					if err := json.NewEncoder(w).Encode(map[string]any{"id": id, "name": "pilot", "links": map[string]string{"self": base + "/cloudCredentials/pilot"}}); err != nil {
						t.Errorf("credential response: %v", err)
					}
					return true
				})
				t.Cleanup(block.unblock)
				config = f.config()
				d := schema.TestResourceDataRaw(t, cloudCredentialFields(), map[string]any{"name": "pilot", "driver": "nutanix"})
				if operation != "create" {
					d.SetId("pilot")
				}
				if operation == "import" {
					d.SetId("pilot.nutanix")
				}
				if phase == "pre_canceled" {
					cancel()
				}
				resource := resourceRancher2CloudCredential()
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
				success := phase == "success" || phase == "already_active" || phase == "snapshot" || phase == "lookup_retry"
				wantID := "pilot"
				if operation == "delete" && success || operation == "create" && !success && phase != "readiness_canceled" {
					wantID = ""
				} else if operation == "import" && !success {
					wantID = "pilot.nutanix"
				}
				wantWrites := int32(0)
				if (operation == "create" || operation == "update" || operation == "delete") && phase != "pre_canceled" && phase != "foreign_id" && phase != "activation_failed" && phase != "activation_canceled" {
					wantWrites = 1
				}
				wantActions := int32(0)
				if operation == "create" && phase != "pre_canceled" && phase != "already_active" {
					wantActions = 1
				}
				gets, mutations := f.counts()
				if diagnostics.HasError() == success || d.Id() != wantID || writes.Load() != wantWrites || actions.Load() != wantActions || mutations != int(wantWrites+wantActions) || phase == "pre_canceled" && gets != 0 {
					t.Fatalf("diagnostics=%v ID=%q want=%q GET=%d writes=%d want=%d actions=%d want=%d", diagnostics, d.Id(), wantID, gets, writes.Load(), wantWrites, actions.Load(), wantActions)
				}
				if strings.HasSuffix(phase, "canceled") && (!diagnostics.HasError() || !strings.Contains(diagnostics[0].Summary, context.Canceled.Error())) {
					t.Fatalf("lost caller cancellation: %v", diagnostics)
				}
			})
		}
	}
}
