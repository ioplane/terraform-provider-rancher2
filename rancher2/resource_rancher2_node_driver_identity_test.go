package rancher2

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/rancher/norman/clientbase"
	managementClient "github.com/rancher/rancher/pkg/client/generated/management/v3"
)

func TestNodeDriverIdentity(t *testing.T) {
	testProvisioningLookupIdentity(t, "nodeDriver")
}

func TestCloudCredentialIdentity(t *testing.T) {
	testProvisioningLookupIdentity(t, "cloudCredential")
	testCloudCredentialContext(t)
}

func TestClusterDriverIdentity(t *testing.T) {
	testProvisioningLookupIdentity(t, "kontainerDriver")
}

func testProvisioningLookupIdentity(t *testing.T, kind string) {
	t.Helper()
	fields := nodeDriverFields
	read, remove, refresh := resourceRancher2ClusterDriverRead, resourceRancher2ClusterDriverDelete, clusterDriverStateRefreshFunc
	switch kind {
	case "nodeDriver":
		read = func(d *schema.ResourceData, meta interface{}) error {
			return resourceRancher2NodeDriverRead(t.Context(), d, meta)
		}
		remove = func(d *schema.ResourceData, meta interface{}) error {
			return resourceRancher2NodeDriverDelete(t.Context(), d, meta)
		}
		refresh = func(client *managementClient.Client, id string) retry.StateRefreshFunc {
			return nodeDriverStateRefreshFunc(t.Context(), &client.APIBaseClient, id)
		}
	case "cloudCredential":
		fields = cloudCredentialFields
		read = func(d *schema.ResourceData, meta interface{}) error {
			return resourceRancher2CloudCredentialRead(t.Context(), d, meta)
		}
		remove = func(d *schema.ResourceData, meta interface{}) error {
			return resourceRancher2CloudCredentialDelete(t.Context(), d, meta)
		}
		refresh = func(client *managementClient.Client, id string) retry.StateRefreshFunc {
			return cloudCredentialStateRefreshFunc(t.Context(), &client.APIBaseClient, id)
		}
	case "kontainerDriver":
		fields = clusterDriverFields
		read, remove, refresh = resourceRancher2ClusterDriverRead, resourceRancher2ClusterDriverDelete, clusterDriverStateRefreshFunc
	}
	for _, action := range []string{"read", "delete", "refresh"} {
		for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
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
							map[string]interface{}{"id": kind, "links": map[string]string{"collection": base + "/" + kind + "s"}, "resourceMethods": []string{"GET", "DELETE"}},
						}}); err != nil {
							t.Errorf("encode schema: %v", err)
						}
						return
					}
					if r.URL.Path != "/v3/"+kind+"s/"+id {
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
				config.contextReady = map[string]*contextClientState{"": {client: &client.APIBaseClient}}
				d := schema.TestResourceDataRaw(t, fields(), map[string]interface{}{"name": id})
				d.SetId(id)
				switch action {
				case "read":
					err = read(d, config)
				case "delete":
					err = remove(d, config)
				case "refresh":
					var state string
					_, state, err = refresh(client, id)()
					if status != http.StatusNotFound && state != "" || status == http.StatusNotFound && state != "removed" {
						t.Errorf("refresh state = %q for HTTP %d", state, status)
					}
				}
				if status != http.StatusNotFound {
					if err == nil || d.Id() != id || d.State() == nil || d.State().ID != id {
						t.Fatalf("HTTP %d must retain identity and return error: ID=%q error=%v", status, d.Id(), err)
					}
					var apiError *clientbase.APIError
					if !errors.As(err, &apiError) || apiError.StatusCode != status {
						t.Fatalf("lookup error lost HTTP %d: %v", status, err)
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
