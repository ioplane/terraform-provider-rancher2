package rancher2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tfprotov5"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/rancher/norman/clientbase"
)

type settingContextFixture struct {
	server          *httptest.Server
	mu              sync.Mutex
	gets, mutations int
	tokens          []string
	hook            func(http.ResponseWriter, *http.Request) bool
	single          bool
}

func TestSettingContextMutations(t *testing.T) {
	for _, operation := range []string{"create", "update", "delete", "reset"} {
		for _, phase := range []string{"success", "pre-canceled", "mutation-canceled", "forbidden"} {
			t.Run(operation+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				var written atomic.Bool
				block := newSettingContextBlock()
				f := newSettingContextFixture(t, true, func(w http.ResponseWriter, r *http.Request) bool {
					if r.URL.Path != "/v3/settings/pilot" && r.URL.Path != "/v3/settings" {
						return false
					}
					if r.Method == http.MethodGet {
						if phase == "forbidden" {
							http.Error(w, "forbidden", http.StatusForbidden)
							return true
						}
						if (operation == "create" && !written.Load()) || (operation == "delete" && written.Load()) {
							http.Error(w, "missing", http.StatusNotFound)
							return true
						}
					} else {
						if phase == "mutation-canceled" {
							cancel()
							block.serve(w, r, false, http.StatusOK)
							return true
						}
						method := http.MethodPut
						switch operation {
						case "create":
							method = http.MethodPost
						case "delete":
							method = http.MethodDelete
						}
						if r.Method != method {
							t.Errorf("mutation method=%s want=%s", r.Method, method)
						}
						if method != http.MethodDelete {
							var payload map[string]interface{}
							if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
								t.Errorf("mutation body: %v", err)
							}
							want := "stable"
							if operation == "reset" {
								want = ""
							}
							if payload["value"] != want {
								t.Errorf("mutation value=%v want=%q", payload["value"], want)
							}
						}
						written.Store(true)
						if method == http.MethodDelete {
							w.WriteHeader(http.StatusNoContent)
							return true
						}
					}
					creator, value := "user", "stable"
					if operation == "reset" {
						creator = ""
						if written.Load() {
							value = ""
						}
					}
					w.Header().Set("Content-Type", "application/json")
					response := map[string]interface{}{"id": "pilot", "name": "pilot", "value": value, "creatorId": creator, "links": map[string]string{"self": "http://" + r.Host + "/v3/settings/pilot"}}
					if err := json.NewEncoder(w).Encode(response); err != nil {
						t.Errorf("response: %v", err)
					}
					return true
				})
				t.Cleanup(block.unblock)
				d := schema.TestResourceDataRaw(t, settingFields(), map[string]interface{}{"name": "pilot", "value": "stable"})
				if operation != "create" {
					d.SetId("pilot")
				}
				if phase == "pre-canceled" {
					cancel()
				}
				resource := resourceRancher2Setting()
				var callback func(context.Context, *schema.ResourceData, interface{}) diag.Diagnostics = resource.UpdateWithoutTimeout
				switch operation {
				case "create":
					callback = resource.CreateWithoutTimeout
				case "delete", "reset":
					callback = resource.DeleteWithoutTimeout
				}
				diags := callback(ctx, d, f.config())
				gets, mutations := f.counts()
				wantID := "pilot"
				if operation == "create" && phase != "success" || (operation == "delete" || operation == "reset") && phase == "success" {
					wantID = ""
				}
				wantMutations := 1
				if phase == "pre-canceled" || phase == "forbidden" {
					wantMutations = 0
				}
				if diags.HasError() != (phase != "success") || d.Id() != wantID || mutations != wantMutations || phase == "pre-canceled" && gets != 0 {
					t.Fatalf("diagnostics=%v ID=%q want=%q GET=%d mutations=%d want=%d", diags, d.Id(), wantID, gets, mutations, wantMutations)
				}
			})
		}
	}
}

func TestSettingContextManagedReadTimeout(t *testing.T) {
	block := newSettingContextBlock()
	f := newSettingContextFixture(t, true, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v3/settings/pilot" {
			return false
		}
		block.serve(w, r, true, http.StatusNotFound)
		return true
	})
	t.Cleanup(block.unblock)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := f.config()
	if _, err := c.settingClientContext(ctx); err != nil {
		t.Fatal(err)
	}
	d := schema.TestResourceDataRaw(t, settingFields(), map[string]interface{}{"name": "pilot", "value": "previous"})
	d.SetId("pilot")
	err := resourceRancher2SettingRead(ctx, d, c)
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || d.Id() != "pilot" || d.Get("value") != "previous" {
		t.Fatalf("404 body timeout changed identity: error=%v caller=%v state=%v", err, ctx.Err(), d.State())
	}
}

func TestSettingContextManagedRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		cold   bool
	}{
		{"canceled", 0, false},
		{"success", http.StatusOK, false},
		{"forbidden", http.StatusForbidden, false},
		{"missing", http.StatusNotFound, false},
		{"initialization-missing", http.StatusNotFound, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSettingContextFixture(t, true, func(w http.ResponseWriter, r *http.Request) bool {
				if tc.status > http.StatusOK && ((!tc.cold && r.URL.Path == "/v3/settings/pilot") || (tc.cold && r.URL.Path == "/ping")) {
					http.Error(w, "lookup failed", tc.status)
					return true
				}
				return false
			})
			d := schema.TestResourceDataRaw(t, settingFields(), map[string]interface{}{"name": "pilot", "value": "previous"})
			d.SetId("pilot")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.status == 0 {
				cancel()
			}
			diags := resourceRancher2Setting().ReadWithoutTimeout(ctx, d, f.config())
			gets, mutations := f.counts()
			if mutations != 0 || (tc.status == 0 && gets != 0) {
				t.Fatalf("unexpected requests: GET=%d mutations=%d", gets, mutations)
			}
			switch {
			case tc.status == http.StatusOK:
				if diags.HasError() || d.Id() != "pilot" || d.Get("value") != "stable" {
					t.Fatalf("successful read: diagnostics=%v state=%v", diags, d.State())
				}
			case tc.status == http.StatusNotFound && !tc.cold:
				if diags.HasError() || d.Id() != "" {
					t.Fatalf("missing setting: diagnostics=%v ID=%q", diags, d.Id())
				}
			default:
				if !diags.HasError() || d.Id() != "pilot" || d.Get("value") != "previous" {
					t.Fatalf("failed read changed identity: diagnostics=%v state=%v", diags, d.State())
				}
			}
		})
	}
}

func TestSettingContextImport(t *testing.T) {
	for _, status := range []int{0, http.StatusOK, http.StatusForbidden, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newSettingContextFixture(t, true, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/v3/settings/pilot" && status > http.StatusOK {
					http.Error(w, "lookup failed", status)
					return true
				}
				return false
			})
			d := schema.TestResourceDataRaw(t, settingFields(), map[string]interface{}{"name": "previous", "value": "previous"})
			d.SetId("pilot")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if status == 0 {
				cancel()
			}
			states, err := resourceRancher2Setting().Importer.StateContext(ctx, d, f.config())
			gets, mutations := f.counts()
			if mutations != 0 {
				t.Fatalf("import performed %d mutations", mutations)
			}
			if status == http.StatusOK {
				if err != nil || len(states) != 1 || states[0] != d || d.Id() != "pilot" || d.Get("name") != "pilot" || d.Get("value") != "stable" {
					t.Fatalf("import failed: states=%v error=%v data=%v", states, err, d.State())
				}
				return
			}
			if err == nil || len(states) != 0 || d.Id() != "pilot" || d.Get("name") != "previous" || d.Get("value") != "previous" {
				t.Fatalf("failed import changed state: states=%v error=%v data=%v", states, err, d.State())
			}
			if status == 0 && (!errors.Is(err, context.Canceled) || gets != 0) {
				t.Fatalf("canceled import: error=%v GET=%d", err, gets)
			}
		})
	}
}

func newSettingContextFixture(t *testing.T, single bool, hook func(http.ResponseWriter, *http.Request) bool) *settingContextFixture {
	t.Helper()
	f := &settingContextFixture{single: single, hook: hook}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		if r.Method == http.MethodGet {
			f.gets++
		} else {
			f.mutations++
		}
		f.tokens = append(f.tokens, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if f.hook != nil && f.hook(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "mutation rejected", http.StatusMethodNotAllowed)
			return
		}
		f.reply(t, w, r)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *settingContextFixture) reply(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.URL.Path == "/ping" {
		if _, err := w.Write([]byte("pong")); err != nil {
			t.Errorf("ping response: %v", err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	base := "http://" + r.Host
	var response interface{}
	switch r.URL.Path {
	case "/v3", "/schemas":
		endpoint := base + "/schemas"
		if f.single {
			endpoint = base + "/v3"
		}
		w.Header().Set("X-API-Schemas", endpoint)
		response = map[string]interface{}{"data": []interface{}{map[string]interface{}{"id": "setting", "links": map[string]string{"collection": base + "/v3/settings"}, "resourceMethods": []string{"GET", "PUT", "DELETE"}, "collectionMethods": []string{"POST"}}}}
	case "/v3/settings/k8s-version":
		response = map[string]string{"id": "k8s-version", "value": "v1.35.0"}
	case "/v3/settings/pilot":
		response = map[string]string{"id": "pilot", "name": "pilot", "value": "stable"}
	case "/k8s/clusters/local/v1", "/k8s/clusters/other/v1":
		endpoint := base + r.URL.Path
		w.Header().Set("X-API-Schemas", endpoint)
		response = map[string]any{"data": []any{map[string]any{
			"id": machineConfigV2NutanixAPIType, "resourceMethods": []string{http.MethodGet},
			"links": map[string]string{"collection": endpoint + "/" + machineConfigV2NutanixAPIType},
		}}}
	case "/k8s/clusters/local/v1/" + machineConfigV2NutanixAPIType + "/fleet-default/nc-context":
		out := *testMachineConfigV2NutanixConf
		out.ID = "fleet-default/nc-context"
		out.Kind = machineConfigV2NutanixKind
		response = &out
	default:
		http.Error(w, "unexpected request", 404)
		return
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Errorf("JSON response: %v", err)
	}
}

func (f *settingContextFixture) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets, f.mutations
}
func (f *settingContextFixture) config() *Config {
	return &Config{URL: f.server.URL, TokenKey: "old-token", Timeout: time.Second}
}

func settingContextRPC(ctx context.Context, t *testing.T, c *Config) (*schema.GRPCProviderServer, *tfprotov5.ReadDataSourceRequest, tftypes.Type) {
	t.Helper()
	p := &schema.Provider{DataSourcesMap: map[string]*schema.Resource{"rancher2_setting": dataSourceRancher2Setting()}}
	p.SetMeta(c)
	rpc := schema.NewGRPCProviderServer(p)
	s, err := rpc.GetProviderSchema(ctx, &tfprotov5.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	ty := s.DataSourceSchemas["rancher2_setting"].ValueType()
	value := tftypes.NewValue(ty, map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.String, nil), "name": tftypes.NewValue(tftypes.String, "pilot"), "value": tftypes.NewValue(tftypes.String, nil)})
	config, err := tfprotov5.NewDynamicValue(ty, value)
	if err != nil {
		t.Fatal(err)
	}
	return rpc, &tfprotov5.ReadDataSourceRequest{TypeName: "rancher2_setting", Config: &config}, ty
}

func settingContextRPCError(resp *tfprotov5.ReadDataSourceResponse) bool {
	for _, d := range resp.Diagnostics {
		if d.Severity == tfprotov5.DiagnosticSeverityError {
			return true
		}
	}
	return false
}

func TestSettingContextSDKPreCanceled(t *testing.T) {
	f := newSettingContextFixture(t, false, nil)
	c := f.config()
	rpc, req, _ := settingContextRPC(context.Background(), t, c)
	for _, warm := range []bool{false, true} {
		if warm {
			resp, err := rpc.ReadDataSource(context.Background(), req)
			if err != nil || settingContextRPCError(resp) {
				t.Fatalf("warm: %v %v", err, resp)
			}
		}
		before, _ := f.counts()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		resp, err := rpc.ReadDataSource(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		after, mutations := f.counts()
		if !settingContextRPCError(resp) || resp.State != nil || after != before || mutations != 0 {
			t.Fatalf("pre-cancel warm=%v error=%v state_nil=%v GET_delta=%d mutations=%d", warm, settingContextRPCError(resp), resp.State == nil, after-before, mutations)
		}
		if !warm && c.contextReady[""] != nil {
			t.Fatal("pre-cancel published partial cache")
		}
	}
	t.Log("actual SDK cold/warm pre-cancel: GET_delta=0 mutations=0 error diagnostics State=nil")
}

func TestSettingContextSDKState(t *testing.T) {
	f := newSettingContextFixture(t, false, nil)
	c := f.config()
	rpc, req, ty := settingContextRPC(context.Background(), t, c)
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	for attempt := range 2 {
		callCtx := context.Background()
		if attempt == 0 {
			callCtx = first
		}
		resp, err := rpc.ReadDataSource(callCtx, req)
		cancelFirst()
		if err != nil || settingContextRPCError(resp) || resp.State == nil {
			t.Fatalf("RPC: %v %v", err, resp)
		}
		value, err := resp.State.Unmarshal(ty)
		if err != nil {
			t.Fatal(err)
		}
		var attrs map[string]tftypes.Value
		if err := value.As(&attrs); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{"id": "pilot", "name": "pilot", "value": "stable"} {
			var got string
			if err := attrs[key].As(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("%s=%q want %q", key, got, want)
			}
		}
	}
	gets, mutations := f.counts()
	if gets != 6 || mutations != 0 {
		t.Fatalf("stable projection GET=%d mutations=%d; want 6/0", gets, mutations)
	}
	if c.Client.Management != nil || c.K8SDefaultVersion != "" {
		t.Fatal("pilot published legacy client/default")
	}
	t.Logf("two stable SDK projections: GET=%d mutations=%d", gets, mutations)
}

type settingContextBlock struct {
	entered, canceled, release chan struct{}
	once                       sync.Once
}

func newSettingContextBlock() *settingContextBlock {
	return &settingContextBlock{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
}
func (b *settingContextBlock) unblock() { b.once.Do(func() { close(b.release) }) }
func (b *settingContextBlock) serve(w http.ResponseWriter, r *http.Request, body bool, status int) {
	if body {
		w.WriteHeader(status)
		if _, err := w.Write([]byte("{")); err != nil {
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	close(b.entered)
	select {
	case <-r.Context().Done():
		close(b.canceled)
	case <-b.release:
	}
}
func settingContextAwait(ctx context.Context, t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatalf("barrier: %v", ctx.Err())
	}
}

func TestSettingContextCancellationPhases(t *testing.T) {
	for _, tc := range []struct {
		name, path   string
		body, single bool
	}{
		{"ping_headers", "/ping", false, false}, {"ping_body", "/ping", true, false},
		{"schema1_headers", "/v3", false, false}, {"schema1_body_single", "/v3", true, true},
		{"schema2_headers", "/schemas", false, false}, {"schema2_body", "/schemas", true, false},
		{"default_headers", "/v3/settings/k8s-version", false, false}, {"default_body", "/v3/settings/k8s-version", true, false},
		{"final_headers", "/v3/settings/pilot", false, false}, {"final_body", "/v3/settings/pilot", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := newSettingContextBlock()
			defer block.unblock()
			f := newSettingContextFixture(t, tc.single, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != tc.path {
					return false
				}
				if tc.body && tc.path == "/v3" {
					w.Header().Set("X-API-Schemas", "http://"+r.Host+"/v3")
				}
				block.serve(w, r, tc.body, 200)
				return true
			})
			c := f.config()
			rpc, req, _ := settingContextRPC(context.Background(), t, c)
			budget := 3 * time.Second
			if tc.body {
				budget = 250 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			var wg sync.WaitGroup
			wg.Add(1)
			defer wg.Wait()
			result := make(chan *tfprotov5.ReadDataSourceResponse, 1)
			errs := make(chan error, 1)
			go func() { defer wg.Done(); resp, err := rpc.ReadDataSource(ctx, req); result <- resp; errs <- err }()
			settingContextAwait(ctx, t, block.entered)
			if !tc.body {
				cancel()
			}
			deadline, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			settingContextAwait(deadline, t, block.canceled)
			select {
			case resp := <-result:
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
				if tc.body {
					for _, d := range resp.Diagnostics {
						if strings.Contains(d.Summary, "Get \"") {
							t.Fatalf("body phase stopped before response-body read: %s", d.Summary)
						}
					}
				}
				if !settingContextRPCError(resp) || resp.State != nil {
					t.Fatalf("canceled RPC: %v", resp)
				}
			case <-deadline.Done():
				t.Fatal("RPC ignored cancellation")
			}
			_, mutations := f.counts()
			if mutations != 0 {
				t.Fatalf("mutations=%d", mutations)
			}
			t.Log("actual SDK caller and HTTP handler observed parent cancellation; State=nil mutations=0")
		})
	}
}

type settingWaitContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *settingWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestSettingContextOwnerWaiters(t *testing.T) {
	for _, cancelOwner := range []bool{true, false} {
		name := "waiter_cancel"
		if cancelOwner {
			name = "owner_cancel"
		}
		t.Run(name, func(t *testing.T) {
			block := newSettingContextBlock()
			defer block.unblock()
			var once sync.Once
			f := newSettingContextFixture(t, false, func(w http.ResponseWriter, r *http.Request) bool {
				blocked := false
				if r.URL.Path == "/ping" {
					once.Do(func() { blocked = true })
				}
				if !blocked {
					return false
				}
				block.serve(w, r, false, 200)
				return false
			})
			c := f.config()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			ownerCtx, stopOwner := context.WithCancel(ctx)
			defer stopOwner()
			owner := make(chan error, 1)
			var wg sync.WaitGroup
			wg.Add(1)
			defer wg.Wait()
			go func() { defer wg.Done(); _, err := c.settingByIDContext(ownerCtx, "pilot"); owner <- err }()
			settingContextAwait(ctx, t, block.entered)
			waiterCtx, stopWaiter := context.WithCancel(ctx)
			defer stopWaiter()
			bctx := &settingWaitContext{Context: waiterCtx, waiting: make(chan struct{})}
			b := make(chan error, 1)
			wg.Add(1)
			go func() { defer wg.Done(); _, err := c.settingByIDContext(bctx, "pilot"); b <- err }()
			settingContextAwait(ctx, t, bctx.waiting)
			cctx := &settingWaitContext{Context: ctx, waiting: make(chan struct{})}
			survivor := make(chan error, 1)
			wg.Add(1)
			go func() { defer wg.Done(); _, err := c.settingByIDContext(cctx, "pilot"); survivor <- err }()
			settingContextAwait(ctx, t, cctx.waiting)
			if cancelOwner {
				stopOwner()
				if err := <-owner; !errors.Is(err, context.Canceled) {
					t.Fatalf("owner error: %v", err)
				}
				if err := <-b; err != nil {
					t.Fatal(err)
				}
			} else {
				stopWaiter()
				if err := <-b; !errors.Is(err, context.Canceled) {
					t.Fatalf("waiter error: %v", err)
				}
				block.unblock()
				if err := <-owner; err != nil {
					t.Fatal(err)
				}
			}
			if err := <-survivor; err != nil {
				t.Fatal(err)
			}
			if _, err := c.settingByIDContext(ctx, "pilot"); err != nil {
				t.Fatal(err)
			}
			if c.contextReady[""] == nil {
				t.Fatal("survivor did not publish reusable client")
			}
			_, mutations := f.counts()
			if mutations != 0 {
				t.Fatal("mutation")
			}
		})
	}
}

func TestSettingContextGeneration(t *testing.T) {
	for _, cancelOld := range []bool{false, true} {
		name := "successful_old_owner"
		if cancelOld {
			name = "canceled_old_owner"
		}
		t.Run(name, func(t *testing.T) {
			oldBlock, newBlock := newSettingContextBlock(), newSettingContextBlock()
			defer oldBlock.unblock()
			defer newBlock.unblock()
			var newOnce sync.Once
			f := newSettingContextFixture(t, false, func(w http.ResponseWriter, r *http.Request) bool {
				if r.Header.Get("Authorization") == "Bearer old-token" && r.URL.Path == "/schemas" {
					oldBlock.serve(w, r, false, 200)
					return r.Context().Err() != nil
				}
				if r.Header.Get("Authorization") == "Bearer new-token" && r.URL.Path == "/v3/settings/k8s-version" {
					newOnce.Do(func() { newBlock.serve(w, r, false, 200) })
					return false
				}
				return false
			})
			c := f.config()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			oldCtx, cancelOldCtx := context.WithCancel(ctx)
			defer cancelOldCtx()
			var wg sync.WaitGroup
			defer wg.Wait()
			old := make(chan error, 1)
			wg.Add(1)
			go func() { defer wg.Done(); _, err := c.settingByIDContext(oldCtx, "pilot"); old <- err }()
			settingContextAwait(ctx, t, oldBlock.entered)
			c.contextMu.Lock()
			changed := c.contextChanged
			generation := c.contextGeneration
			c.contextMu.Unlock()
			c.Sync.Lock()
			locked := true
			defer func() {
				if locked {
					c.Sync.Unlock()
				}
			}()
			update := make(chan error, 1)
			wg.Add(1)
			go func() { defer wg.Done(); update <- c.UpdateToken("new-token") }()
			settingContextAwait(ctx, t, changed)
			for {
				c.contextMu.Lock()
				ready := c.contextGeneration >= generation+2
				next := c.contextChanged
				c.contextMu.Unlock()
				if ready {
					break
				}
				settingContextAwait(ctx, t, next)
			}
			fresh := make(chan error, 1)
			wg.Add(1)
			go func() { defer wg.Done(); _, err := c.settingByIDContext(ctx, "pilot"); fresh <- err }()
			settingContextAwait(ctx, t, newBlock.entered)
			c.contextMu.Lock()
			flight := c.contextFlight[""]
			c.contextMu.Unlock()
			if cancelOld {
				cancelOldCtx()
			}
			oldBlock.unblock()
			if err := <-old; err == nil || (cancelOld && !errors.Is(err, context.Canceled)) {
				t.Fatal("old generation succeeded")
			}
			c.contextMu.Lock()
			same := c.contextFlight[""] == flight
			c.contextMu.Unlock()
			if !same {
				t.Fatal("old owner cleared new flight")
			}
			newBlock.unblock()
			if err := <-fresh; err != nil {
				t.Fatal(err)
			}
			c.Sync.Unlock()
			locked = false
			if err := <-update; err != nil {
				t.Fatal(err)
			}
			if _, err := c.settingByIDContext(ctx, "pilot"); err != nil {
				t.Fatal(err)
			}
			_, mutations := f.counts()
			if mutations != 0 {
				t.Fatal("mutation")
			}
			t.Log("token invalidation precedes occupied legacy Sync; stale owner cannot clear new flight")
		})
	}
}

func TestSettingContextLegacyMutex(t *testing.T) {
	f := newSettingContextFixture(t, false, nil)
	c := f.config()
	c.Sync.Lock()
	defer c.Sync.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.settingByIDContext(ctx, "pilot"); err != nil {
		t.Fatalf("pilot entered legacy Sync: %v", err)
	}
}

func TestSettingContextFailures(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		status     int
		body       string
	}{
		{"schema", "/schemas", 200, "{"}, {"default_401", "/v3/settings/k8s-version", 401, "denied"}, {"default_503", "/v3/settings/k8s-version", 503, "unavailable"}, {"final_403", "/v3/settings/pilot", 403, "denied"}, {"final_404", "/v3/settings/pilot", 404, "absent"}, {"final_decode", "/v3/settings/pilot", 200, "{"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			failed := true
			failures := 0
			f := newSettingContextFixture(t, false, func(w http.ResponseWriter, r *http.Request) bool {
				mu.Lock()
				defer mu.Unlock()
				if !failed || r.URL.Path != tc.path {
					return false
				}
				failures++
				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Errorf("failure response: %v", err)
				}
				return true
			})
			c := f.config()
			rpc, req, _ := settingContextRPC(context.Background(), t, c)
			resp, err := rpc.ReadDataSource(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if !settingContextRPCError(resp) || resp.State != nil {
				t.Fatalf("error RPC state: %v", resp)
			}
			if tc.path != "/v3/settings/pilot" && c.contextReady[""] != nil {
				t.Fatal("partial initialization cached")
			}
			mu.Lock()
			failed = false
			count := failures
			mu.Unlock()
			if count != 1 {
				t.Fatalf("unexpected retry count=%d", count)
			}
			resp, err = rpc.ReadDataSource(context.Background(), req)
			if err != nil || settingContextRPCError(resp) || resp.State == nil {
				t.Fatalf("recovery: %v %v", err, resp)
			}
		})
	}
}

func TestSettingContextErrorBodyCauses(t *testing.T) {
	for _, status := range []int{403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			block := newSettingContextBlock()
			defer block.unblock()
			f := newSettingContextFixture(t, false, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/v3/settings/k8s-version" {
					return false
				}
				block.serve(w, r, true, status)
				return true
			})
			c := f.config()
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			var wg sync.WaitGroup
			wg.Add(1)
			defer wg.Wait()
			go func() { defer wg.Done(); _, err := c.settingByIDContext(ctx, "pilot"); result <- err }()
			settingContextAwait(ctx, t, block.entered)
			err := <-result
			var apiErr *clientbase.APIError
			if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.Body != "Unreadable body." {
				t.Fatalf("lost status/context/body cause: %v", err)
			}
			if c.contextReady[""] != nil {
				t.Fatal("failed default cached")
			}
		})
	}
}

func TestSettingContextTimeouts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
		ready   bool
	}{
		{"positive", 40 * time.Millisecond, false}, {"zero_first_probe", 0, true}, {"negative_first_probe", -time.Second, true}, {"zero_failure", 0, false}, {"negative_failure", -time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSettingContextFixture(t, false, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/ping" || tc.ready {
					return false
				}
				if _, err := w.Write([]byte("not-ready")); err != nil {
					t.Errorf("ping: %v", err)
				}
				return true
			})
			c := f.config()
			c.Timeout = tc.timeout
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := c.settingByIDContext(ctx, "pilot")
			if tc.ready && err != nil {
				t.Fatal(err)
			}
			if !tc.ready && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline cause: %v", err)
			}
			gets, mutations := f.counts()
			if tc.timeout <= 0 && !tc.ready && gets != 1 {
				t.Fatalf("first-probe GET=%d", gets)
			}
			if mutations != 0 {
				t.Fatal("mutation")
			}
		})
	}
	t.Run("shorter_parent", func(t *testing.T) {
		block := newSettingContextBlock()
		defer block.unblock()
		f := newSettingContextFixture(t, false, func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/ping" {
				return false
			}
			block.serve(w, r, false, 200)
			return true
		})
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		_, err := f.config().settingByIDContext(ctx, "pilot")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("parent: %v", err)
		}
	})
}

func TestSettingContextPriorState(t *testing.T) {
	f := newSettingContextFixture(t, false, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v3/settings/pilot" {
			return false
		}
		http.Error(w, "denied", http.StatusForbidden)
		return true
	})
	d := schema.TestResourceDataRaw(t, dataSourceRancher2Setting().Schema, map[string]interface{}{"name": "pilot", "value": "previous"})
	d.SetId("previous-id")
	diags := dataSourceRancher2SettingReadContext(context.Background(), d, f.config())
	if !diags.HasError() || d.Id() != "previous-id" || d.Get("value") != "previous" {
		t.Fatalf("prior state changed: ID=%q value=%v diagnostics=%v", d.Id(), d.Get("value"), diags)
	}
	for _, meta := range []interface{}{nil, (*Config)(nil), "synthetic-metadata"} {
		data := schema.TestResourceDataRaw(t, dataSourceRancher2Setting().Schema, map[string]interface{}{"name": "pilot", "value": "previous"})
		data.SetId("previous-id")
		diagnostics := dataSourceRancher2SettingReadContext(context.Background(), data, meta)
		if !diagnostics.HasError() || data.Id() != "previous-id" || data.Get("value") != "previous" {
			t.Fatalf("invalid meta changed prior state: ID=%q value=%v diagnostics=%v", data.Id(), data.Get("value"), diagnostics)
		}
		if strings.Contains(diagnostics[0].Summary, "synthetic-metadata") {
			t.Fatal("invalid meta diagnostic exposed its value")
		}
	}
}

func TestSettingContextWarmSnapshot(t *testing.T) {
	block := newSettingContextBlock()
	defer block.unblock()
	var mu sync.Mutex
	armed := false
	f := newSettingContextFixture(t, false, func(w http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		blocked := armed && r.URL.Path == "/v3/settings/pilot" && r.Header.Get("Authorization") == "Bearer old-token"
		if blocked {
			armed = false
		}
		mu.Unlock()
		if blocked {
			block.serve(w, r, false, 200)
		}
		return false
	})
	c := f.config()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.settingByIDContext(ctx, "pilot"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	armed = true
	mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Wait()
	old := make(chan error, 1)
	go func() { defer wg.Done(); _, err := c.settingByIDContext(ctx, "pilot"); old <- err }()
	settingContextAwait(ctx, t, block.entered)
	if err := c.UpdateToken("new-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.settingByIDContext(ctx, "pilot"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-old:
		t.Fatalf("restart interrupted active old snapshot: %v", err)
	default:
	}
	block.unblock()
	if err := <-old; err != nil {
		t.Fatal(err)
	}
	_, mutations := f.counts()
	if mutations != 0 {
		t.Fatal("mutation")
	}
}

func TestSettingContextDefaultRetry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
		status  int
		recover bool
	}{
		{"positive_500_recovery", 6 * time.Second, 500, true},
		{"zero_403_first_probe", 0, 403, false},
		{"negative_500_first_probe", -time.Second, 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			attempts := 0
			var first, last time.Time
			f := newSettingContextFixture(t, false, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/v3/settings/k8s-version" {
					return false
				}
				mu.Lock()
				attempts++
				n := attempts
				last = time.Now()
				if n == 1 {
					first = last
				}
				mu.Unlock()
				if n > 1 && tc.recover {
					return false
				}
				http.Error(w, "retryable status", tc.status)
				return true
			})
			c := f.config()
			c.Timeout = tc.timeout
			_, err := c.settingByIDContext(context.Background(), "pilot")
			mu.Lock()
			n, pause := attempts, last.Sub(first)
			mu.Unlock()
			if tc.recover {
				if err != nil || n != 2 || pause < 5*time.Second {
					t.Fatalf("retry err=%v attempts=%d pause=%s", err, n, pause)
				}
			} else {
				var apiErr *clientbase.APIError
				if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status || n != 1 {
					t.Fatalf("first-probe err=%v attempts=%d", err, n)
				}
				if c.contextReady[""] != nil {
					t.Fatal("failed default cached")
				}
			}
			_, mutations := f.counts()
			if mutations != 0 {
				t.Fatal("mutation")
			}
		})
	}
}

// The bounded deadline exercises retry-wait causes; next-guard retention is source-reviewed separately.
func TestSettingContextRetryErrorCauses(t *testing.T) {
	for _, phase := range []string{"readiness", "default"} {
		t.Run(phase, func(t *testing.T) {
			f := newSettingContextFixture(t, false, func(w http.ResponseWriter, r *http.Request) bool {
				if phase == "readiness" && r.URL.Path == "/ping" {
					hijacker, ok := w.(http.Hijacker)
					if !ok {
						t.Error("HTTP fixture does not support connection ownership")
						return true
					}
					conn, _, err := hijacker.Hijack()
					if err != nil {
						t.Errorf("hijack readiness connection: %v", err)
						return true
					}
					if err := conn.Close(); err != nil {
						t.Errorf("close readiness connection: %v", err)
					}
					return true
				}
				if phase == "default" && r.URL.Path == "/v3/settings/k8s-version" {
					http.Error(w, "retry cause", http.StatusInternalServerError)
					return true
				}
				return false
			})
			c := f.config()
			c.Timeout = 75 * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := c.settingByIDContext(ctx, "pilot")
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("phase deadline cause lost: %v", err)
			}
			wantGET := 1
			if phase == "readiness" {
				var network *url.Error
				if !errors.As(err, &network) || network.URL != f.server.URL+"/ping" || !errors.Is(err, io.EOF) {
					t.Fatalf("readiness network cause lost: %v", err)
				}
			} else {
				wantGET = 4
				var apiErr *clientbase.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
					t.Fatalf("default status cause lost: %v", err)
				}
			}
			gets, mutations := f.counts()
			if gets != wantGET || mutations != 0 {
				t.Fatalf("retry boundary GET=%d mutations=%d; want %d/0", gets, mutations, wantGET)
			}
			t.Logf("%s retry-wait deadline retains original cause: GET=%d mutations=%d; next-guard schedule unmeasured", phase, gets, mutations)
		})
	}
}
