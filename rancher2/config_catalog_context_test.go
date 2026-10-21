package rancher2

import (
	"context"
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
	for _, name := range []string{"pre_cancel", "initialization_404", "resource_404", "canceled_404_body", "retry_500"} {
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
