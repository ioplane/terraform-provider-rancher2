package rancher2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/rancher/norman/clientbase"
	"github.com/rancher/norman/types"
	catalogv1 "github.com/rancher/rancher/pkg/apis/catalog.cattle.io/v1"
	catalog "github.com/rancher/terraform-provider-rancher2/internal/ranchercatalog"
)

type catalogContractTransport func(*http.Request) (*http.Response, error)

func (f catalogContractTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func catalogBoundarySnapshot(t *testing.T) map[string]interface{} {
	t.Helper()
	result := map[string]interface{}{}
	for name, fields := range map[string]map[string]*schema.Schema{
		"pool":     clusterV2RKEConfigMachinePoolFields(),
		"defaults": clusterV2RKEConfigMachinePoolDefaultFields(),
	} {
		s := fields["hostname_length_limit"]
		checks := map[string]interface{}{}
		for _, v := range []int{9, 10, 63, 64} {
			warnings, errs := s.ValidateFunc(v, "hostname_length_limit")
			messages := []string{}
			for _, err := range errs {
				messages = append(messages, err.Error())
			}
			checks[fmt.Sprint(v)] = map[string]interface{}{"warnings": warnings, "errors": messages}
		}
		result[name] = map[string]interface{}{
			"type": s.Type, "optional": s.Optional, "required": s.Required, "computed": s.Computed,
			"forceNew": s.ForceNew, "default": s.Default, "sensitive": s.Sensitive,
			"description": s.Description, "deprecated": s.Deprecated, "checks": checks,
		}
	}
	const base = "https://catalog.invalid"
	for _, action := range []string{"install", "upgrade", "info"} {
		for _, malformed := range []bool{false, true} {
			var calls []string
			var requests []string
			transport := catalogContractTransport(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.Method+" "+r.URL.String())
				if r.Body != nil {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						return nil, fmt.Errorf("read fixture request: %w", err)
					}
					requests = append(requests, string(body))
				}
				var body string
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/repos/fixture":
					payload := map[string]interface{}{
						"id": "fixture", "type": catalogV2APIType,
						"links":  map[string]string{"info": base + "/info?link=info"},
						"status": map[string]interface{}{"conditions": []map[string]string{{"type": string(catalogv1.RepoDownloaded), "status": "True"}}},
					}
					b, err := json.Marshal(payload)
					if err != nil {
						return nil, fmt.Errorf("marshal catalog fixture: %w", err)
					}
					body = string(b)
				case r.Method == http.MethodGet && r.URL.Path == "/info":
					body = `{"readme":"readme","appReadme":"app","values":{"nested":{"number":42}},"questions":{},"chart":{"name":"fixture"}}`
					if malformed {
						body = "{"
					}
				case r.Method == http.MethodPost && r.URL.Path == "/"+action:
					body = `{"operationName":"op-1","operationNamespace":"cattle-system"}`
					if malformed {
						body = "{"
					}
				default:
					return nil, fmt.Errorf("unexpected fixture request: %s %s", r.Method, r.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			ops := &clientbase.APIOperations{
				Opts:   &clientbase.ClientOpts{},
				Client: &http.Client{Transport: transport, Timeout: time.Second},
				Types:  map[string]types.Schema{catalogV2APIType: {ID: catalogV2APIType, ResourceMethods: []string{"GET"}, Links: map[string]string{"collection": base + "/repos"}}},
			}
			client := &clientbase.APIBaseClient{Ops: ops, Types: ops.Types, Opts: ops.Opts}
			config := &Config{Timeout: time.Second, Client: Client{CatalogV2: map[string]*clientbase.APIBaseClient{"c-fixture": client}}}
			repo := &ClusterRepo{Resource: types.Resource{ID: "fixture", Type: catalogV2APIType, Actions: map[string]string{action: base + "/" + action}}}
			var response interface{}
			var err error
			switch action {
			case "install":
				d := schema.TestResourceDataRaw(t, appV2Fields(), testAppV2InstallInterface)
				input, expandErr := expandChartInstallActionV2(d, testAppV2ChartInfo)
				if expandErr != nil {
					t.Fatalf("expand install fixture: %v", expandErr)
				}
				response, err = createAppV2(config, "c-fixture", repo, input)
			case "upgrade":
				d := schema.TestResourceDataRaw(t, appV2Fields(), testAppV2Interface)
				input, expandErr := expandChartUpgradeActionV2(d, testAppV2ChartInfo)
				if expandErr != nil {
					t.Fatalf("expand upgrade fixture: %v", expandErr)
				}
				response, err = upgradeAppV2(config, "c-fixture", repo, input)
			case "info":
				var info *catalog.ChartInfo
				_, info, err = infoAppV2(config, "c-fixture", "fixture", "chart name", "1.0.0")
				response = info
			}
			message := ""
			if err != nil {
				message = err.Error()
			}
			if malformed && err == nil {
				t.Fatalf("%s accepted malformed JSON", action)
			}
			if !malformed && err != nil {
				t.Fatalf("%s fixture: %v", action, err)
			}
			result[fmt.Sprintf("%s/invalid=%t", action, malformed)] = map[string]interface{}{"calls": calls, "requests": requests, "response": response, "error": message}
		}
	}
	return result
}

func TestCatalogAndHostnameOriginalContract(t *testing.T) {
	actual, err := json.MarshalIndent(catalogBoundarySnapshot(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("testdata/catalog-hostname-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(actual, '\n'), expected) {
		t.Fatalf("catalog callers or hostname validation differ from the original provider:\n%s", actual)
	}
}
