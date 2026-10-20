package rancher2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	projectClient "github.com/rancher/rancher/pkg/client/generated/project/v3"
)

func TestGetRegistryByFiltersValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		filters   map[string]interface{}
		kind      string
		query     string
		namespace string
	}{
		{name: "nil_filters"},
		{name: "missing_name", filters: map[string]interface{}{"projectId": "c-synthetic:p-synthetic"}},
		{name: "non_string_name", filters: map[string]interface{}{"name": 42, "projectId": "c-synthetic:p-synthetic"}},
		{name: "missing_project_id", filters: map[string]interface{}{"name": "synthetic"}},
		{name: "non_string_project_id", filters: map[string]interface{}{"name": "synthetic", "projectId": 42}},
		{name: "empty_name", filters: map[string]interface{}{"name": "", "projectId": "c-synthetic:p-synthetic"}},
		{name: "empty_project_id", filters: map[string]interface{}{"name": "synthetic", "projectId": ""}},
		{name: "project_list", filters: map[string]interface{}{"name": "synthetic", "projectId": "c-synthetic:p-synthetic"}, kind: "dockerCredential", query: "name=synthetic&projectId=c-synthetic%3Ap-synthetic"},
		{name: "namespaced_list", filters: map[string]interface{}{"name": "synthetic", "projectId": "c-synthetic:p-synthetic", "namespaceId": "ns-synthetic"}, kind: "namespacedDockerCredential", query: "name=synthetic&namespaceId=ns-synthetic&projectId=c-synthetic%3Ap-synthetic", namespace: "ns-synthetic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const projectPath = "/v3/projects/c-synthetic:p-synthetic"
			var mu sync.Mutex
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, r.Method+" "+r.URL.RequestURI())
				if tc.kind == "" || r.Method != http.MethodGet {
					t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.RequestURI())
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.URL.Path == "/ping" && r.URL.RawQuery == "" {
					if _, err := w.Write([]byte("pong")); err != nil {
						t.Errorf("write readiness response: %v", err)
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				baseURL := "http://" + r.Host
				var response interface{}
				switch {
				case r.URL.Path == projectPath && r.URL.RawQuery == "":
					w.Header().Set("X-API-Schemas", baseURL+projectPath)
					response = map[string]interface{}{"data": []interface{}{
						map[string]interface{}{"id": "dockerCredential", "links": map[string]string{"collection": baseURL + projectPath + "/dockerCredentials"}, "collectionMethods": []string{"GET"}},
						map[string]interface{}{"id": "namespacedDockerCredential", "links": map[string]string{"collection": baseURL + projectPath + "/namespacedDockerCredentials"}, "collectionMethods": []string{"GET"}},
					}}
				case r.URL.Path == projectPath+"/"+tc.kind+"s" && r.URL.RawQuery == tc.query:
					response = map[string]interface{}{"data": []interface{}{
						map[string]string{"id": "registry-synthetic", "type": tc.kind, "name": "synthetic", "projectId": "c-synthetic:p-synthetic", "namespaceId": tc.namespace},
					}}
				default:
					t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.RequestURI())
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Errorf("encode registry response: %v", err)
				}
			}))
			defer srv.Close()
			config := &Config{URL: srv.URL, Timeout: time.Second}
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("GetRegistryByFilters panicked: %v", recovered)
				}
				mu.Lock()
				defer mu.Unlock()
				if tc.kind == "" {
					if len(calls) != 0 || config.Client.Project != nil || config.Client.Management != nil || config.Client.Cluster != nil || config.Client.CatalogV2 != nil {
						t.Errorf("invalid filters initialized a client or sent HTTP: calls=%v clients=%#v", calls, config.Client)
					}
					t.Logf("invalid filters: HTTP requests=%d project client initialized=%t", len(calls), config.Client.Project != nil)
					return
				}
				wantCalls := []string{"GET /ping", "GET " + projectPath, "GET " + projectPath + "/" + tc.kind + "s?" + tc.query}
				if !reflect.DeepEqual(calls, wantCalls) {
					t.Errorf("request sequence = %v, want %v", calls, wantCalls)
				}
				if len(config.Client.Project) != 1 || config.Client.Project["c-synthetic:p-synthetic"] == nil {
					t.Error("expected one generated project client at the supplied project ID")
				}
				t.Logf("request sequence: %v", calls)
			}()

			result, err := config.GetRegistryByFilters(tc.filters)
			if tc.kind == "" {
				if result != nil || err == nil || err.Error() != "[ERROR] Name nor project_id can't be nil" {
					t.Errorf("invalid filters: result=%#v error=%v, want nil result and original validation error", result, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("list registries: %v", err)
			}
			switch collection := result.(type) {
			case *projectClient.DockerCredentialCollection:
				if tc.kind != "dockerCredential" || len(collection.Data) != 1 {
					t.Fatalf("project collection = %#v, want one project registry", collection)
				}
				registry := collection.Data[0]
				if registry.ID != "registry-synthetic" || registry.Type != "dockerCredential" || registry.Name != "synthetic" || registry.ProjectID != "c-synthetic:p-synthetic" || registry.NamespaceId != "" {
					t.Errorf("project registry payload = %#v", registry)
				}
			case *projectClient.NamespacedDockerCredentialCollection:
				if tc.kind != "namespacedDockerCredential" || len(collection.Data) != 1 {
					t.Fatalf("namespaced collection = %#v, want one namespaced registry", collection)
				}
				registry := collection.Data[0]
				if registry.ID != "registry-synthetic" || registry.Type != "namespacedDockerCredential" || registry.Name != "synthetic" || registry.ProjectID != "c-synthetic:p-synthetic" || registry.NamespaceId != "ns-synthetic" {
					t.Errorf("namespaced registry payload = %#v", registry)
				}
			default:
				t.Fatalf("registry collection type = %T", result)
			}
			t.Logf("typed collection: %T; registry identity and scope preserved", result)
		})
	}
}
