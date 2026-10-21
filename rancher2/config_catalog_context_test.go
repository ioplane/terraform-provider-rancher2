package rancher2

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/stretchr/testify/require"
)

func TestCatalogContextFactory(t *testing.T) {
	t.Run("pre_cancel", func(t *testing.T) {
		f := newSettingContextFixture(t, true, nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := f.config().catalogClientContext(ctx, "local")
		require.ErrorIs(t, err, context.Canceled)
		gets, mutations := f.counts()
		require.Zero(t, gets)
		require.Zero(t, mutations)
	})
	t.Run("scope_and_invalidation", func(t *testing.T) {
		f := newSettingContextFixture(t, true, nil)
		c := f.config()
		catalog, err := c.catalogClientContext(t.Context(), "local")
		require.NoError(t, err)
		gets, _ := f.counts()
		require.Equal(t, 2, gets, "catalog needs ping and schema, not a management default version")
		management, err := c.settingClientContext(t.Context())
		require.NoError(t, err)
		other, err := c.catalogClientContext(t.Context(), "other")
		require.NoError(t, err)
		require.NotSame(t, catalog, management)
		require.NotSame(t, catalog, other)
		require.Equal(t, f.server.URL+"/k8s/clusters/local/v1", catalog.Opts.URL)
		before, _ := f.counts()
		warm, err := c.catalogClientContext(t.Context(), "local")
		require.NoError(t, err)
		require.Same(t, catalog, warm)
		after, _ := f.counts()
		require.Equal(t, before, after)
		require.NoError(t, c.UpdateToken("new-token"))
		for _, key := range []string{"", "local", "other"} {
			require.Nil(t, c.contextReady[key], "invalidation must detach every API scope")
		}
		fresh, err := c.catalogClientContext(t.Context(), "local")
		require.NoError(t, err)
		require.NotSame(t, catalog, fresh)
		require.Equal(t, "old-token", catalog.Opts.TokenKey)
		require.Equal(t, "new-token", fresh.Opts.TokenKey)
	})
	for _, name := range []string{"schema_cancel", "waiter_isolation"} {
		t.Run(name, func(t *testing.T) {
			block := newSettingContextBlock()
			var calls atomic.Int32
			f := newSettingContextFixture(t, true, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/k8s/clusters/local/v1" {
					return false
				}
				calls.Add(1)
				block.serve(w, r, name == "schema_cancel", http.StatusOK)
				return name == "schema_cancel"
			})
			c := f.config()
			c.Timeout = 5 * time.Second
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			ownerCtx, stopOwner := context.WithCancel(ctx)
			defer stopOwner()
			var wg sync.WaitGroup
			defer wg.Wait()
			c.Sync.Lock()
			defer c.Sync.Unlock()
			defer block.unblock()
			owner := make(chan error, 1)
			wg.Add(1)
			go func() { defer wg.Done(); _, err := c.catalogClientContext(ownerCtx, "local"); owner <- err }()
			settingContextAwait(ctx, t, block.entered)
			if name == "schema_cancel" {
				stopOwner()
				require.ErrorIs(t, <-owner, context.Canceled)
				settingContextAwait(ctx, t, block.canceled)
				return
			}
			waitCtx, stopWaiter := context.WithCancel(ctx)
			defer stopWaiter()
			waiting := &settingWaitContext{Context: waitCtx, waiting: make(chan struct{})}
			waiter := make(chan error, 1)
			wg.Add(1)
			go func() { defer wg.Done(); _, err := c.catalogClientContext(waiting, "local"); waiter <- err }()
			settingContextAwait(ctx, t, waiting.waiting)
			stopWaiter()
			require.ErrorIs(t, <-waiter, context.Canceled)
			_, err := c.catalogClientContext(ctx, "other")
			require.NoError(t, err, "another key must not wait for the local owner")
			_, err = c.settingClientContext(ctx)
			require.NoError(t, err, "management must not share the local initialization flight")
			require.EqualValues(t, 1, calls.Load(), "waiters must not start duplicate schema reads")
			block.unblock()
			require.NoError(t, <-owner)
		})
	}
}

func TestMachineConfigContextRead(t *testing.T) {
	const id = "fleet-default/nc-context"
	resourcePath := "/k8s/clusters/local/v1/" + machineConfigV2NutanixAPIType + "/" + id
	for _, name := range []string{"pre_cancel", "initialization_404", "resource_404", "canceled_404_body", "retry_500", "foreign_identity"} {
		t.Run(name, func(t *testing.T) {
			block := newSettingContextBlock()
			var resourceCalls atomic.Int32
			f := newSettingContextFixture(t, true, func(w http.ResponseWriter, r *http.Request) bool {
				if name == "initialization_404" && r.URL.Path == "/k8s/clusters/local/v1" {
					http.Error(w, "schema unavailable", http.StatusNotFound)
					return true
				}
				if r.URL.Path != resourcePath {
					return false
				}
				count := resourceCalls.Add(1)
				switch name {
				case "foreign_identity":
					out := *testMachineConfigV2NutanixConf
					out.ID = "fleet-default/foreign"
					if err := json.NewEncoder(w).Encode(&out); err != nil {
						t.Errorf("foreign identity response: %v", err)
					}
					return true
				case "resource_404":
					http.Error(w, "resource absent", http.StatusNotFound)
					return true
				case "canceled_404_body":
					block.serve(w, r, true, http.StatusNotFound)
					return true
				case "retry_500":
					if count == 1 {
						http.Error(w, "resource temporarily unavailable", http.StatusInternalServerError)
						return true
					}
				}
				return false
			})
			c := f.config()
			c.Timeout = 10 * time.Second
			if name == "initialization_404" {
				c.Timeout = 0
			}
			d := schema.TestResourceDataRaw(t, machineConfigV2Fields(), map[string]any{
				"generate_name": "context", "nutanix_config": []any{map[string]any{"cluster": "previous", "vm_image": "previous", "vm_network": []any{"previous"}}},
			})
			require.NoError(t, d.Set("kind", machineConfigV2NutanixKind))
			d.SetId(id)
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			readCtx, stopRead := context.WithCancel(ctx)
			defer stopRead()
			if name == "pre_cancel" {
				stopRead()
			}
			var wg sync.WaitGroup
			defer func() {
				stopRead()
				block.unblock()
				wg.Wait()
			}()
			result := make(chan diag.Diagnostics, 1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				result <- resourceRancher2MachineConfigV2().ReadWithoutTimeout(readCtx, d, c)
			}()
			if name == "canceled_404_body" {
				settingContextAwait(ctx, t, block.entered)
				stopRead()
			}
			diagnostics := <-result
			_, mutations := f.counts()
			require.Zero(t, mutations)
			switch name {
			case "resource_404":
				require.False(t, diagnostics.HasError())
				require.Empty(t, d.Id())
			case "retry_500":
				require.False(t, diagnostics.HasError(), "%v", diagnostics)
				require.EqualValues(t, 2, resourceCalls.Load())
				require.Equal(t, testMachineConfigV2NutanixConf.Cluster, d.Get("nutanix_config.0.cluster"))
			default:
				require.True(t, diagnostics.HasError())
				require.Equal(t, id, d.Id())
				require.Equal(t, "previous", d.Get("nutanix_config.0.cluster"))
				if name == "pre_cancel" {
					gets, _ := f.counts()
					require.Zero(t, gets)
				}
			}
		})
	}
}

func TestMachineConfigContextDelete(t *testing.T) {
	const id = "fleet-default/nc-context"
	resourcePath := "/k8s/clusters/local/v1/" + machineConfigV2NutanixAPIType + "/" + id
	for _, name := range []string{"pre_cancel", "initialization_404", "mutation_canceled", "mutation_404", "wait_canceled", "success", "snapshot_invalidation", "foreign_identity"} {
		t.Run(name, func(t *testing.T) {
			block := newSettingContextBlock()
			var deleted atomic.Bool
			var polls atomic.Int32
			var c *Config
			var f *settingContextFixture
			f = newSettingContextFixture(t, true, func(w http.ResponseWriter, r *http.Request) bool {
				if name == "initialization_404" && r.URL.Path == "/k8s/clusters/local/v1" {
					http.Error(w, "schema unavailable", http.StatusNotFound)
					return true
				}
				if r.URL.Path != resourcePath {
					return false
				}
				if r.Header.Get("Authorization") != "Bearer old-token" {
					t.Errorf("resource request changed credential snapshot")
				}
				if name == "foreign_identity" && r.Method == http.MethodGet {
					out := *testMachineConfigV2NutanixConf
					out.ID = "fleet-default/foreign"
					out.Type = machineConfigV2NutanixAPIType
					out.Links = map[string]string{"self": "http://" + r.Host + "/foreign"}
					if err := json.NewEncoder(w).Encode(&out); err != nil {
						t.Errorf("foreign identity response: %v", err)
					}
					return true
				}
				if r.Method == http.MethodDelete {
					deleted.Store(true)
					switch name {
					case "mutation_canceled":
						block.serve(w, r, true, http.StatusOK)
					case "mutation_404":
						http.Error(w, "uncertain delete response", http.StatusNotFound)
					default:
						w.WriteHeader(http.StatusNoContent)
					}
					return true
				}
				if deleted.Load() {
					poll := polls.Add(1)
					if name == "wait_canceled" {
						block.serve(w, r, true, http.StatusOK)
						return true
					}
					if name != "success" || poll > 1 {
						http.Error(w, "resource absent", http.StatusNotFound)
						return true
					}
				} else if name == "snapshot_invalidation" {
					f.reply(t, w, r)
					if err := c.UpdateToken("new-token"); err != nil {
						t.Errorf("invalidate snapshot: %v", err)
					}
					return true
				}
				return false
			})
			c = f.config()
			c.Timeout = 5 * time.Second
			if name == "initialization_404" {
				c.Timeout = 0
			}
			d := schema.TestResourceDataRaw(t, machineConfigV2Fields(), map[string]any{
				"generate_name": "context", "nutanix_config": []any{map[string]any{"cluster": "previous", "vm_image": "previous", "vm_network": []any{"previous"}}},
			})
			require.NoError(t, d.Set("kind", machineConfigV2NutanixKind))
			d.SetId(id)
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			deleteCtx, stopDelete := context.WithCancel(ctx)
			if name == "pre_cancel" {
				stopDelete()
			}
			var wg sync.WaitGroup
			defer func() { stopDelete(); block.unblock(); wg.Wait() }()
			result := make(chan diag.Diagnostics, 1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				result <- resourceRancher2MachineConfigV2().DeleteContext(deleteCtx, d, c)
			}()
			if name == "mutation_canceled" || name == "wait_canceled" {
				settingContextAwait(ctx, t, block.entered)
				stopDelete()
			}
			diagnostics := <-result
			gets, mutations := f.counts()
			switch name {
			case "pre_cancel", "initialization_404", "foreign_identity":
				require.Zero(t, mutations)
			default:
				require.Equal(t, 1, mutations, "uncertain mutations must never be replayed")
			}
			if name == "success" || name == "snapshot_invalidation" {
				require.False(t, diagnostics.HasError(), "%v", diagnostics)
				require.Empty(t, d.Id())
				if name == "success" {
					require.EqualValues(t, 2, polls.Load(), "an object still present must remain pending")
				} else {
					require.Nil(t, c.contextReady["local"], "Delete must not acquire a new generation after invalidation")
				}
			} else {
				require.True(t, diagnostics.HasError())
				require.Equal(t, id, d.Id())
				require.Equal(t, "previous", d.Get("nutanix_config.0.cluster"))
				if name == "pre_cancel" {
					require.Zero(t, gets)
				}
				if name == "mutation_canceled" || name == "mutation_404" {
					require.Zero(t, polls.Load())
				}
			}
		})
	}
}

func TestMachineConfigContextUpdate(t *testing.T) {
	const id = "fleet-default/nc-context"
	resourcePath := "/k8s/clusters/local/v1/" + machineConfigV2NutanixAPIType + "/" + id
	for _, name := range []string{"pre_cancel", "mutation_canceled", "empty_identity", "different_identity", "refresh_canceled", "refresh_404", "snapshot_invalidation"} {
		t.Run(name, func(t *testing.T) {
			block := newSettingContextBlock()
			var mutated atomic.Bool
			var c *Config
			var f *settingContextFixture
			f = newSettingContextFixture(t, true, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != resourcePath {
					return false
				}
				if r.Header.Get("Authorization") != "Bearer old-token" {
					t.Errorf("resource request changed credential snapshot")
				}
				if r.Method == http.MethodPut {
					mutated.Store(true)
					switch name {
					case "mutation_canceled":
						block.serve(w, r, true, http.StatusOK)
						return true
					case "empty_identity", "different_identity":
						out := *testMachineConfigV2NutanixConf
						out.ID = ""
						if name == "different_identity" {
							out.ID = "fleet-default/another"
						}
						if err := json.NewEncoder(w).Encode(&out); err != nil {
							t.Errorf("update response: %v", err)
						}
						return true
					}
					f.reply(t, w, r)
					return true
				}
				if r.Method == http.MethodGet && mutated.Load() {
					switch name {
					case "refresh_canceled":
						block.serve(w, r, true, http.StatusOK)
						return true
					case "refresh_404":
						http.Error(w, "resource absent after update", http.StatusNotFound)
						return true
					}
				}
				if name == "snapshot_invalidation" && !mutated.Load() {
					f.reply(t, w, r)
					if err := c.UpdateToken("new-token"); err != nil {
						t.Errorf("invalidate snapshot: %v", err)
					}
					return true
				}
				return false
			})
			c = f.config()
			c.Timeout = 5 * time.Second
			d := schema.TestResourceDataRaw(t, machineConfigV2Fields(), map[string]any{
				"generate_name": "context", "nutanix_config": []any{map[string]any{"cluster": "previous", "vm_image": "previous", "vm_network": []any{"previous"}}},
			})
			require.NoError(t, d.Set("kind", machineConfigV2NutanixKind))
			d.SetId(id)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			updateCtx, stopUpdate := context.WithCancel(ctx)
			if name == "pre_cancel" {
				stopUpdate()
			}
			var wg sync.WaitGroup
			defer func() { stopUpdate(); block.unblock(); wg.Wait() }()
			result := make(chan diag.Diagnostics, 1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				result <- resourceRancher2MachineConfigV2().UpdateContext(updateCtx, d, c)
			}()
			if name == "mutation_canceled" || name == "refresh_canceled" {
				settingContextAwait(ctx, t, block.entered)
				stopUpdate()
			}
			diagnostics := <-result
			gets, mutations := f.counts()
			require.Equal(t, id, d.Id())
			if name == "pre_cancel" {
				require.Zero(t, gets)
				require.Zero(t, mutations)
			} else {
				require.Equal(t, 1, mutations, "an uncertain update must not be replayed")
			}
			if name == "snapshot_invalidation" {
				require.False(t, diagnostics.HasError(), "%v", diagnostics)
				require.Nil(t, c.contextReady["local"])
				require.Equal(t, testMachineConfigV2NutanixConf.Cluster, d.Get("nutanix_config.0.cluster"))
			} else {
				require.True(t, diagnostics.HasError())
				require.Equal(t, "previous", d.Get("nutanix_config.0.cluster"))
			}
		})
	}
}

func TestMachineConfigContextCreate(t *testing.T) {
	const id = "fleet-default/nc-context"
	collectionPath := "/k8s/clusters/local/v1/" + machineConfigV2NutanixAPIType
	for _, name := range []string{"pre_cancel", "mutation_canceled", "empty_identity", "wait_canceled", "deadline", "rbac_recovery", "snapshot_invalidation"} {
		t.Run(name, func(t *testing.T) {
			block := newSettingContextBlock()
			var schemas atomic.Int32
			var c *Config
			var f *settingContextFixture
			f = newSettingContextFixture(t, true, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/k8s/clusters/local/v1" {
					if schemas.Add(1) == 1 && name == "rbac_recovery" {
						endpoint := "http://" + r.Host + r.URL.Path
						w.Header().Set("X-API-Schemas", endpoint)
						response := map[string]any{"data": []any{map[string]any{
							"id": machineConfigV2NutanixAPIType, "resourceMethods": []string{http.MethodPut}, "collectionMethods": []string{http.MethodPost},
							"links": map[string]string{"collection": endpoint + "/" + machineConfigV2NutanixAPIType},
						}}}
						if err := json.NewEncoder(w).Encode(response); err != nil {
							t.Errorf("RBAC schema response: %v", err)
						}
						return true
					}
					return false
				}
				if r.URL.Path != collectionPath && r.URL.Path != collectionPath+"/"+id {
					return false
				}
				if r.Header.Get("Authorization") != "Bearer old-token" {
					t.Errorf("create request changed credential snapshot")
				}
				if r.Method == http.MethodPost {
					switch name {
					case "mutation_canceled":
						block.serve(w, r, true, http.StatusOK)
						return true
					case "empty_identity":
						if _, err := w.Write([]byte(`{}`)); err != nil {
							t.Errorf("empty create response: %v", err)
						}
						return true
					case "snapshot_invalidation":
						f.reply(t, w, r)
						if err := c.UpdateToken("new-token"); err != nil {
							t.Errorf("invalidate snapshot: %v", err)
						}
						return true
					}
					f.reply(t, w, r)
					return true
				}
				if r.Method == http.MethodGet && (name == "wait_canceled" || name == "deadline") {
					block.serve(w, r, true, http.StatusOK)
					return true
				}
				return false
			})
			c = f.config()
			c.Timeout = 5 * time.Second
			d := schema.TestResourceDataRaw(t, machineConfigV2Fields(), map[string]any{
				"generate_name": "context", "nutanix_config": []any{map[string]any{"cluster": "previous", "vm_image": "previous", "vm_network": []any{"previous"}}},
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			createCtx, stopCreate := context.WithCancel(ctx)
			if name == "deadline" {
				createCtx, stopCreate = context.WithTimeout(ctx, time.Second)
			}
			if name == "pre_cancel" {
				stopCreate()
			}
			var wg sync.WaitGroup
			defer func() { stopCreate(); block.unblock(); wg.Wait() }()
			result := make(chan diag.Diagnostics, 1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				result <- resourceRancher2MachineConfigV2().CreateContext(createCtx, d, c)
			}()
			if name == "mutation_canceled" || name == "wait_canceled" {
				settingContextAwait(ctx, t, block.entered)
				stopCreate()
			}
			diagnostics := <-result
			gets, mutations := f.counts()
			if name == "pre_cancel" {
				require.Zero(t, gets)
				require.Zero(t, mutations)
			} else {
				require.Equal(t, 1, mutations, "an uncertain create must not be replayed")
			}
			if name == "pre_cancel" || name == "mutation_canceled" || name == "empty_identity" {
				require.True(t, diagnostics.HasError())
				require.Empty(t, d.Id(), "an unknown generated ID must not be invented")
				return
			}
			require.Equal(t, id, d.Id())
			if name == "wait_canceled" || name == "deadline" {
				require.True(t, diagnostics.HasError())
				require.Equal(t, "previous", d.Get("nutanix_config.0.cluster"))
				return
			}
			require.False(t, diagnostics.HasError(), "%v", diagnostics)
			require.Equal(t, testMachineConfigV2NutanixConf.Cluster, d.Get("nutanix_config.0.cluster"))
			if name == "rbac_recovery" {
				require.EqualValues(t, 2, schemas.Load(), "RBAC recovery must rediscover schemas without another POST")
			} else {
				require.Nil(t, c.contextReady["local"])
				require.EqualValues(t, 1, schemas.Load(), "invalidation alone must not switch an active operation's snapshot")
			}
		})
	}
}
