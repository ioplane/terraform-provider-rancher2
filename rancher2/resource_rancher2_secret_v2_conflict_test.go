package rancher2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/rancher/norman/clientbase"
	"github.com/rancher/norman/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUpdateSecretV2ConflictRefreshesResourceVersion(t *testing.T) {
	const id = "default/synthetic"
	type payload struct {
		Type     string            `json:"type"`
		K8SType  string            `json:"_type"`
		Metadata metav1.ObjectMeta `json:"metadata"`
		Data     map[string]string `json:"data"`
	}
	var mu sync.Mutex
	var calls []string
	var updates []payload
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		var response interface{}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1":
			w.Header().Set("X-API-Schemas", baseURL+"/v1")
			response = types.SchemaCollection{Data: []types.Schema{{
				ID: "secret", Links: map[string]string{"collection": baseURL + "/v1/secrets"},
				ResourceMethods: []string{"GET", "PUT"},
			}}}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/secrets/"+id:
			response = map[string]interface{}{
				"id": id, "type": "Opaque", "_type": "Opaque",
				"links":    map[string]string{"self": baseURL + "/v1/secrets/" + id},
				"metadata": map[string]string{"name": "synthetic", "namespace": "default", "resourceVersion": "11"},
			}
		case r.Method == http.MethodPut && r.URL.Path == "/v1/secrets/"+id && len(updates) < 2:
			var body payload
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode update request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			updates = append(updates, body)
			if len(updates) == 1 {
				w.WriteHeader(http.StatusConflict)
				response = map[string]string{"type": "error", "code": "Conflict", "message": "synthetic version conflict"}
				break
			}
			// Accept even a stale retry so the assertion exposes it without repeated conflicts.
			response = map[string]interface{}{
				"id": id, "type": "Opaque", "_type": "Opaque",
				"metadata": map[string]string{"name": "synthetic", "namespace": "default", "resourceVersion": "12"},
				"data":     map[string]string{"key": "c3ludGhldGlj"},
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("encode fixture response: %v", err)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL

	client, err := clientbase.NewAPIClient(&clientbase.ClientOpts{URL: srv.URL + "/v1", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("initialize Norman client: %v", err)
	}
	config := &Config{
		Timeout: 30 * time.Second,
		Client:  Client{CatalogV2: map[string]*clientbase.APIBaseClient{"c-synthetic": &client}},
	}
	secret := &SecretV2{
		Resource: types.Resource{ID: id, Type: "secret"},
		Secret: corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "synthetic", Namespace: "default", ResourceVersion: "10",
				Labels: map[string]string{"app": "fixture"}, Annotations: map[string]string{"owner": "fixture"},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{"key": []byte("synthetic")},
		},
		K8SType: "Opaque",
	}
	result, err := updateSecretV2(config, "c-synthetic", id, secret)
	if err != nil {
		t.Fatalf("update secret after conflict: %v", err)
	}
	if result.ID != id || result.ResourceVersion != "12" || result.K8SType != "Opaque" || string(result.Data["key"]) != "synthetic" {
		t.Fatal("updated secret identity, version, type or data was not decoded")
	}
	if secret.ResourceVersion != "11" {
		t.Fatalf("conflict refresh resourceVersion = %q, want %q", secret.ResourceVersion, "11")
	}
	mu.Lock()
	defer mu.Unlock()
	wantCalls := []string{
		"GET /v1", "GET /v1/secrets/" + id, "PUT /v1/secrets/" + id,
		"GET /v1/secrets/" + id, "GET /v1/secrets/" + id, "PUT /v1/secrets/" + id,
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("request sequence = %v, want %v", calls, wantCalls)
	}
	if len(updates) != 2 {
		t.Fatalf("PUT count = %d, want 2", len(updates))
	}
	for i, body := range updates {
		if body.Type != "Opaque" || body.K8SType != "Opaque" || body.Metadata.Name != "synthetic" || body.Metadata.Namespace != "default" ||
			!reflect.DeepEqual(body.Metadata.Labels, map[string]string{"app": "fixture"}) ||
			!reflect.DeepEqual(body.Metadata.Annotations, map[string]string{"owner": "fixture"}) ||
			!reflect.DeepEqual(body.Data, map[string]string{"key": "c3ludGhldGlj"}) {
			t.Errorf("PUT %d did not preserve secret type, data and metadata", i+1)
		}
	}
	if updates[0].Metadata.ResourceVersion != "10" {
		t.Errorf("first PUT resourceVersion = %q, want %q", updates[0].Metadata.ResourceVersion, "10")
	}
	if updates[1].Metadata.ResourceVersion != "11" {
		t.Errorf("retry PUT resourceVersion = %q, want refreshed %q", updates[1].Metadata.ResourceVersion, "11")
	}
	t.Logf("PUT versions: %s -> %s; GET count: 3; PUT count: 2", updates[0].Metadata.ResourceVersion, updates[1].Metadata.ResourceVersion)
}
