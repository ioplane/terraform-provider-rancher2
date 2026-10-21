package rancher2

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSDKOwnerStateAndID(t *testing.T) {
	r := resourceRancher2ClusterV2()
	selector := map[string]any{
		"config":                 map[string]any{"protect-kernel-defaults": true, "kubelet-arg": []any{"max-pods=110"}},
		"machine_label_selector": []any{map[string]any{"match_labels": map[string]any{"role": "worker"}}},
	}
	state := map[string]any{
		"id": "fleet-default/synthetic-cluster", "name": "synthetic-cluster", "untouched": "preserved",
		"rke_config": []any{map[string]any{"machine_selector_config": []any{selector}}},
	}
	upgraded, err := r.StateUpgraders[0].Upgrade(context.Background(), state, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, r.SchemaVersion)
	assert.Equal(t, 0, r.StateUpgraders[0].Version)
	assert.Equal(t, "fleet-default/synthetic-cluster", upgraded["id"])
	assert.Equal(t, "synthetic-cluster", upgraded["name"])
	assert.Equal(t, "preserved", upgraded["untouched"])
	// Captured from the SDKv1 provider before the owner transition.
	assert.Equal(t, "kubelet-arg:\n- max-pods=110\nprotect-kernel-defaults: true\n", selector["config"])
	assert.Equal(t, []any{map[string]any{"match_labels": map[string]any{"role": "worker"}}}, selector["machine_label_selector"])
	first, second := splitID("c-synthetic.default:test")
	assert.Equal(t, "c-synthetic", first)
	assert.Equal(t, "default:test", second)
}

func TestSDKOwnerComputedSet(t *testing.T) {
	resources := map[string]*schema.Resource{
		"resource":    resourceRancher2GlobalRole(),
		"data_source": dataSourceRancher2GlobalRole(),
	}
	for name, r := range resources {
		t.Run(name, func(t *testing.T) {
			first := map[string]any{"namespace": "first", "rules": []any{map[string]any{"verbs": []any{"get"}, "resources": []any{"pods"}}}}
			second := map[string]any{"namespace": "second", "rules": []any{map[string]any{"verbs": []any{"list"}, "resources": []any{"secrets"}}}}
			d := schema.TestResourceDataRaw(t, r.Schema, map[string]any{"name": "synthetic-role"})
			require.NoError(t, d.Set("inherited_namespaced_rules", []any{first, second, first}))
			set := d.Get("inherited_namespaced_rules").(*schema.Set)
			require.Equal(t, 2, set.Len())
			got := make(map[string]any)
			for _, value := range set.List() {
				entry := value.(map[string]any)
				rules := entry["rules"].([]any)
				require.Len(t, rules, 1)
				rule := rules[0].(map[string]any)
				got[entry["namespace"].(string)] = map[string]any{"verbs": rule["verbs"], "resources": rule["resources"]}
			}
			assert.Equal(t, map[string]any{
				"first":  map[string]any{"verbs": []any{"get"}, "resources": []any{"pods"}},
				"second": map[string]any{"verbs": []any{"list"}, "resources": []any{"secrets"}},
			}, got)
		})
	}
}

func TestSDKOwnerSecretForceNew(t *testing.T) {
	tests := []struct {
		name                        string
		oldValue, newValue, wantNew bool
	}{
		{"false_to_false", false, false, false},
		{"false_to_true", false, true, false},
		{"true_to_false", true, false, true},
		{"true_to_true", true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resourceRancher2SecretV2()
			d := schema.TestResourceDataRaw(t, r.Schema, map[string]any{
				"cluster_id": "c-synthetic", "name": "test", "immutable": tt.oldValue, "data": map[string]any{"value": "old"},
			})
			d.SetId("c-synthetic.default:test")
			config := terraform.NewResourceConfigRaw(map[string]any{
				"cluster_id": "c-synthetic", "name": "test", "immutable": tt.newValue, "data": map[string]any{"value": "new"},
			})
			diff, err := r.Diff(context.Background(), d.State(), config, nil)
			require.NoError(t, err)
			require.NotNil(t, diff)
			assert.Equal(t, tt.wantNew, diff.RequiresNew())
			require.Contains(t, diff.Attributes, "data.value")
			assert.True(t, diff.Attributes["data.value"].Sensitive)
			assert.Equal(t, "default", d.Get("namespace"))
			assert.Equal(t, "Opaque", d.Get("type"))
			assert.Equal(t, "c-synthetic.default:test", d.State().ID)
		})
	}
}
