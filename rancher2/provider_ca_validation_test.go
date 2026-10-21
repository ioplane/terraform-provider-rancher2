package rancher2

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/rancher/norman/clientbase"
)

func TestProviderRejectsInvalidCACerts(t *testing.T) {
	cases := []struct {
		name     string
		ca       string
		insecure bool
		reject   bool
	}{
		{name: "invalid_text", ca: "synthetic-invalid-ca", reject: true},
		{name: "invalid_certificate", ca: "-----BEGIN CERTIFICATE-----\nYQ==\n-----END CERTIFICATE-----", reject: true},
		{name: "whitespace", ca: " \n\t", reject: true},
		{name: "invalid_insecure", ca: "synthetic-invalid-ca", insecure: true, reject: true},
		{name: "empty_ca"},
		{name: "valid_ca"},
		{name: "valid_bundle_with_unrelated_data"},
		{name: "empty_ca_insecure", insecure: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests, handshakes atomic.Int64
			const token = "synthetic-provider-ca-token"
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("Norman request lost method or configured authentication")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				baseURL := "https://" + r.Host
				w.Header().Set("Content-Type", "application/json")
				var response interface{}
				switch r.URL.Path {
				case "/v3":
					w.Header().Set("X-API-Schemas", baseURL+"/v3")
					response = map[string]interface{}{"type": "collection", "data": []interface{}{
						map[string]interface{}{"id": "setting", "type": "schema", "resourceMethods": []string{"GET"}, "links": map[string]string{"collection": baseURL + "/v3/settings"}},
					}}
				case "/v3/settings/server-version":
					response = map[string]interface{}{"id": "server-version", "type": "setting", "value": "synthetic-version"}
				default:
					t.Error("unexpected Norman request path")
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Errorf("encode local response: %v", err)
				}
			}))
			server.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
				handshakes.Add(1)
				return nil, nil
			}}
			server.StartTLS()
			defer server.Close()
			ca := tc.ca
			if tc.name == "valid_ca" || tc.name == "valid_bundle_with_unrelated_data" {
				ca = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
			}
			if tc.name == "valid_bundle_with_unrelated_data" {
				ca = "unrelated text\n" + ca + "-----BEGIN UNRELATED-----\nYQ==\n-----END UNRELATED-----\n"
			}
			p := Provider()
			d := schema.TestResourceDataRaw(t, p.Schema, map[string]interface{}{
				"api_url": server.URL, "token_key": token, "ca_certs": ca, "insecure": tc.insecure, "bootstrap": false, "timeout": "3s",
			})
			raw, err := providerConfigure(d)
			t.Cleanup(func() { t.Logf("http_requests=%d tls_handshake_attempts=%d", requests.Load(), handshakes.Load()) })
			if tc.reject {
				if err == nil {
					t.Fatal("provider Configure accepted invalid ca_certs")
				}
				if !strings.Contains(err.Error(), "ca_certs") {
					t.Error("validation error does not identify ca_certs")
				}
				for _, input := range []string{ca, token, server.URL} {
					if input != "" && strings.Contains(err.Error(), input) {
						t.Error("validation error exposes configured input")
					}
				}
				if c, ok := raw.(*Config); ok && c != nil && (c.URL != "" || c.TokenKey != "" || c.CACerts != "" || c.Client.Management != nil || len(c.Client.Cluster) != 0 || len(c.Client.Project) != 0 || len(c.Client.CatalogV2) != 0) {
					t.Error("rejection returned usable configuration or initialized clients")
				}
				if requests.Load() != 0 || handshakes.Load() != 0 {
					t.Error("rejected configuration reached local endpoint")
				}
				return
			}
			if err != nil {
				t.Fatal("provider Configure rejected accepted CA control")
			}
			c, ok := raw.(*Config)
			if !ok || c == nil {
				t.Fatal("provider Configure returned no configuration")
			}
			if c.URL != server.URL || c.TokenKey != token || c.CACerts != ca || c.Insecure != tc.insecure || c.Timeout != 3*time.Second {
				t.Fatal("accepted configuration lost URL, authentication, CA, insecure or timeout options")
			}
			if c.Client.Management != nil || len(c.Client.Cluster) != 0 || len(c.Client.Project) != 0 || len(c.Client.CatalogV2) != 0 || requests.Load() != 0 || handshakes.Load() != 0 {
				t.Fatal("Configure eagerly initialized a client or contacted the endpoint")
			}
			opts := c.CreateClientOpts()
			if opts.URL != server.URL || opts.TokenKey != token || opts.CACerts != ca || opts.Insecure != tc.insecure || opts.Timeout != 3*time.Second {
				t.Fatal("consumer options lost configured values")
			}
			opts.URL += "/v3"
			owned := &http.Client{Timeout: 3 * time.Second}
			defer owned.CloseIdleConnections()
			opts.HTTPClient = owned
			client, err := clientbase.NewAPIClient(opts)
			if tc.name == "empty_ca" {
				var unknownAuthority x509.UnknownAuthorityError
				if !errors.As(err, &unknownAuthority) {
					t.Fatal("empty secure CA did not retain ordinary certificate verification")
				}
				if requests.Load() != 0 || handshakes.Load() != 1 || client.Ops != nil || client.Opts != nil {
					t.Fatal("empty secure CA unexpectedly reached HTTP or initialized a usable Norman client")
				}
				return
			}
			if err != nil {
				t.Fatalf("initialize Norman with accepted CA control: %v", err)
			}
			if client.Ops == nil || client.Opts == nil {
				t.Fatal("Norman returned an uninitialized client")
			}
			var setting struct {
				Value string `json:"value"`
			}
			if err := client.ByID("setting", "server-version", &setting); err != nil {
				t.Fatalf("consume local schema: %v", err)
			}
			if setting.Value != "synthetic-version" || requests.Load() != 2 || handshakes.Load() != 1 {
				t.Fatal("accepted CA control did not preserve authenticated schema consumption over TLS")
			}
		})
	}
}
