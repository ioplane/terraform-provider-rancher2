package rancher2

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/rancher/norman/clientbase"
	"github.com/rancher/norman/types"
)

func TestMachineConfigV2ReadIdentity(t *testing.T) {
	const id = "fleet-default/machine-config-read-fixture"
	for _, tc := range []struct {
		name   string
		status int
		lookup bool
	}{
		{name: "forbidden", status: http.StatusForbidden, lookup: true},
		{name: "not_found", status: http.StatusNotFound, lookup: true},
		{name: "lookup_denied"},
		{name: "bad_request", status: http.StatusBadRequest, lookup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			transport := catalogContractTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/configs/"+id {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{
					StatusCode: tc.status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"type":"error","message":"synthetic lookup failure"}`)),
					Request:    r,
				}, nil
			})
			methods := []string{}
			if tc.lookup {
				methods = append(methods, http.MethodGet)
			}
			ops := &clientbase.APIOperations{
				Opts:   &clientbase.ClientOpts{},
				Client: &http.Client{Transport: transport, Timeout: time.Second},
				Types: map[string]types.Schema{machineConfigV2NutanixAPIType: {
					ID: machineConfigV2NutanixAPIType, ResourceMethods: methods,
					Links: map[string]string{"collection": "https://machine-config.invalid/configs"},
				}},
			}
			client := &clientbase.APIBaseClient{Ops: ops, Types: ops.Types, Opts: ops.Opts}
			config := &Config{Timeout: time.Second, Client: Client{CatalogV2: map[string]*clientbase.APIBaseClient{"local": client}}}
			config.contextReady = map[string]*contextClientState{"local": {client: client}}
			d := schema.TestResourceDataRaw(t, map[string]*schema.Schema{
				"kind": {Type: schema.TypeString, Required: true},
				"name": {Type: schema.TypeString, Optional: true},
			}, map[string]any{"kind": machineConfigV2NutanixKind})
			d.SetId(id)
			err := resourceRancher2MachineConfigV2Read(d, config)
			if tc.status == http.StatusNotFound {
				if err != nil || d.Id() != "" || d.State() != nil {
					t.Fatalf("404 Read: err=%v ID=%q state=%v", err, d.Id(), d.State())
				}
			} else if err == nil || d.Id() != id || d.State() == nil || d.State().ID != id {
				t.Fatalf("failed Read must preserve identity: err=%v ID=%q state=%v", err, d.Id(), d.State())
			}
			wantCalls := 0
			if tc.lookup {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Errorf("lookup calls=%d, want %d", calls, wantCalls)
			}
			calls = 0
			d.SetId(id)
			err = resourceRancher2MachineConfigV2Delete(d, config)
			if tc.status == http.StatusNotFound {
				if err != nil || d.Id() != "" || d.State() != nil {
					t.Fatalf("404 Delete: err=%v ID=%q state=%v", err, d.Id(), d.State())
				}
			} else if err == nil || d.Id() != id || d.State() == nil || d.State().ID != id {
				t.Fatalf("failed Delete lookup must preserve identity: err=%v ID=%q state=%v", err, d.Id(), d.State())
			}
			if calls != wantCalls {
				t.Errorf("Delete lookup calls=%d, want %d with no DELETE", calls, wantCalls)
			}
			calls = 0
			_, state, err := machineConfigV2StateRefreshFunc(config, id, machineConfigV2NutanixKind)()
			if tc.status == http.StatusNotFound {
				if err != nil || state != "removed" {
					t.Fatalf("404 refresh: err=%v state=%q", err, state)
				}
			} else if err == nil || state != "" {
				t.Fatalf("failed refresh must not confirm a state: err=%v state=%q", err, state)
			}
			if calls != wantCalls {
				t.Errorf("refresh lookup calls=%d, want %d", calls, wantCalls)
			}
		})
	}
}

func TestWaitForMachineConfigV2TimeoutPreservesIdentity(t *testing.T) {
	const id = "fleet-default/machine-config-timeout-fixture"
	d := schema.TestResourceDataRaw(t, map[string]*schema.Schema{
		"kind": {Type: schema.TypeString, Required: true},
	}, map[string]interface{}{
		"kind": "UnsupportedTimeoutFixtureConfig",
	})
	d.SetId(id)

	err := waitForMachineConfigV2(d, &Config{}, 0)
	if err == nil {
		t.Fatal("expected machine config wait timeout")
	}
	if !strings.Contains(err.Error(), id) {
		t.Errorf("timeout diagnostic %q does not contain machine config ID %q", err, id)
	}
	if d.Id() != id {
		t.Errorf("machine config ID after timeout = %q, want %q", d.Id(), id)
	}
	state := d.State()
	if state == nil {
		t.Fatal("machine config state after timeout is nil")
	}
	if state.ID != id {
		t.Errorf("machine config state ID after timeout = %q, want %q", state.ID, id)
	}
}
