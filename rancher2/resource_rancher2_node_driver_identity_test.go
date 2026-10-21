package rancher2

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/rancher/norman/clientbase"
	managementClient "github.com/rancher/rancher/pkg/client/generated/management/v3"
)

func TestNodeDriverIdentity(t *testing.T) {
	for _, action := range []string{"read", "delete", "refresh"} {
		for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
			t.Run(fmt.Sprintf("%s_%d", action, status), func(t *testing.T) {
				const id = "synthetic-driver"
				var reads atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if r.URL.Path == "/v3" {
						base := "http://" + r.Host + "/v3"
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("X-API-Schemas", base)
						if err := json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{
							map[string]interface{}{"id": "nodeDriver", "links": map[string]string{"collection": base + "/nodeDrivers"}, "resourceMethods": []string{"GET", "DELETE"}},
						}}); err != nil {
							t.Errorf("encode schema: %v", err)
						}
						return
					}
					if r.URL.Path != "/v3/nodeDrivers/"+id {
						t.Errorf("unexpected request: %s", r.URL.Path)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					reads.Add(1)
					http.Error(w, "synthetic lookup failure", status)
				}))
				t.Cleanup(srv.Close)
				client, err := managementClient.NewClient(&clientbase.ClientOpts{URL: srv.URL + "/v3", Timeout: time.Second})
				if err != nil {
					t.Fatalf("initialize fixture client: %v", err)
				}
				config := &Config{Client: Client{Management: client}}
				d := schema.TestResourceDataRaw(t, nodeDriverFields(), map[string]interface{}{"active": true, "builtin": false, "name": id, "url": "https://example.invalid/driver"})
				d.SetId(id)
				switch action {
				case "read":
					err = resourceRancher2NodeDriverRead(d, config)
				case "delete":
					err = resourceRancher2NodeDriverDelete(d, config)
				case "refresh":
					var state string
					_, state, err = nodeDriverStateRefreshFunc(client, id)()
					if status == http.StatusForbidden && state != "" || status == http.StatusNotFound && state != "removed" {
						t.Errorf("refresh state = %q for HTTP %d", state, status)
					}
				}
				if status == http.StatusForbidden {
					if err == nil || d.Id() != id || d.State() == nil || d.State().ID != id {
						t.Fatalf("HTTP 403 must retain identity and return error: ID=%q error=%v", d.Id(), err)
					}
				} else if err != nil || action != "refresh" && d.Id() != "" {
					t.Fatalf("HTTP 404 must confirm absence: ID=%q error=%v", d.Id(), err)
				}
				if reads.Load() != 1 {
					t.Errorf("resource reads = %d, want 1", reads.Load())
				}
			})
		}
	}
}
