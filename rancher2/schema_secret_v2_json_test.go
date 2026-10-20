package rancher2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/rancher/norman/clientbase"
	"github.com/rancher/norman/types"
	corev1 "k8s.io/api/core/v1"
)

// Characterize the JSON boundary used by Secret V2 CRUD, including both embedded type fields.
func TestSecretV2JSONWire(t *testing.T) {
	const id = "default/synthetic"
	d := schema.TestResourceDataRaw(t, secretV2Fields(), map[string]interface{}{
		"cluster_id": "c-synthetic", "name": "synthetic", "namespace": "default",
		"type": "Opaque", "immutable": true, "data": map[string]interface{}{"key": "synthetic"},
		"labels": map[string]interface{}{"app": "fixture"}, "annotations": map[string]interface{}{"owner": "fixture"},
		"resource_version": "10",
	})
	d.SetId("c-synthetic." + id)
	secret := expandSecretV2(d)
	secret.Resource.Type = "secret"
	secret.Secret.Type = corev1.SecretTypeTLS
	secret.Data = map[string][]byte{"key": []byte("synthetic")}
	secret.Links = map[string]string{"self": "synthetic-link"}
	secret.Actions = map[string]string{"rotate": "synthetic-action"}
	raw, err := interfaceToMap(secret)
	if err != nil {
		t.Fatalf("encode expanded secret: %v", err)
	}
	if _, ok := raw["type"]; ok {
		t.Fatal("unset outer type must omit conflicting embedded types")
	}
	if secret.Resource.Type != "secret" || secret.Secret.Type != corev1.SecretTypeTLS || raw["_type"] != "Opaque" {
		t.Fatal("embedded type identity or Kubernetes wire type changed")
	}
	previous := *secret
	if err := json.Unmarshal([]byte(`{"type":{"ignored":true},"metadata":{"resourceVersion":"11"}}`), &previous); err != nil {
		t.Fatalf("decode formerly ignored JSON type: %v", err)
	}
	wantPrevious := *secret
	wantPrevious.ResourceVersion = "11"
	if !reflect.DeepEqual(previous, wantPrevious) {
		t.Fatal("partial response changed previous fields beyond resourceVersion")
	}
	var mu sync.Mutex
	var calls []string
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		response := interface{}(map[string]interface{}{
			"id": id, "type": "Opaque", "_type": "Opaque", "links": map[string]string{"self": baseURL + "/v1/secrets/" + id},
			"actions": raw["actions"], "metadata": raw["metadata"], "data": raw["data"], "stringData": raw["stringData"], "immutable": true,
		})
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1":
			w.Header().Set("X-API-Schemas", baseURL+"/v1")
			response = types.SchemaCollection{Data: []types.Schema{{ID: "secret", Links: map[string]string{"collection": baseURL + "/v1/secrets"}, CollectionMethods: []string{"POST"}, ResourceMethods: []string{"GET", "PUT", "DELETE"}}}}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/secrets", r.Method == http.MethodPut && r.URL.Path == "/v1/secrets/"+id:
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode secret request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			want := make(map[string]interface{}, len(raw)+1)
			for key, value := range raw {
				want[key] = value
			}
			want["type"] = "Opaque"
			if !reflect.DeepEqual(body, want) {
				t.Error("CRUD payload lost identity, links, metadata, immutable, data, stringData or type/_type")
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/secrets/"+id:
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/secrets/"+id:
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL
	client, err := clientbase.NewAPIClient(&clientbase.ClientOpts{URL: srv.URL + "/v1", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("initialize Norman client: %v", err)
	}
	config := &Config{Timeout: 10 * time.Second, Client: Client{CatalogV2: map[string]*clientbase.APIBaseClient{"c-synthetic": &client}}}
	created, err := createSecretV2(config, "c-synthetic", secret)
	if err != nil {
		t.Fatalf("create secret: %v", err)
	}
	updated, err := updateSecretV2(config, "c-synthetic", id, secret)
	if err != nil {
		t.Fatalf("update secret: %v", err)
	}
	for _, result := range []*SecretV2{created, updated} {
		if result.ID != id || result.Links["self"] != baseURL+"/v1/secrets/"+id || result.K8SType != "Opaque" || result.ResourceVersion != "10" || result.Immutable == nil || !*result.Immutable || string(result.Data["key"]) != "synthetic" || result.StringData["key"] != "synthetic" {
			t.Fatal("response decoding lost secret fields")
		}
		if err := flattenSecretV2(d, result); err != nil {
			t.Fatalf("flatten response: %v", err)
		}
		if d.Id() != "c-synthetic."+id || d.Get("type") != "Opaque" || d.Get("resource_version") != "10" {
			t.Fatal("flattened identity, type or version changed")
		}
	}
	roundTrip, err := interfaceToMap(updated)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	_, hasType := roundTrip["type"]
	if hasType {
		t.Fatal("response round trip must omit the conflicting embedded type")
	}
	delete(roundTrip, "type")
	wantRoundTrip := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		wantRoundTrip[key] = value
	}
	delete(wantRoundTrip, "kind")
	delete(wantRoundTrip, "apiVersion")
	wantRoundTrip["links"] = map[string]interface{}{"self": baseURL + "/v1/secrets/" + id}
	if !reflect.DeepEqual(roundTrip, wantRoundTrip) {
		t.Fatal("response round trip lost fields other than the formerly ambiguous type")
	}
	t.Logf("decoded response re-encodes type: %t; remaining wire fields preserved", hasType)
	if err := deleteSecretV2(config, "c-synthetic", updated); err != nil {
		t.Fatalf("delete secret using response links: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	wantCalls := []string{"GET /v1", "POST /v1/secrets", "GET /v1/secrets/" + id, "PUT /v1/secrets/" + id, "DELETE /v1/secrets/" + id}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("CRUD request sequence = %v, want %v", calls, wantCalls)
	}
}
