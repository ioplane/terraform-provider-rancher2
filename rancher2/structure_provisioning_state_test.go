package rancher2

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	managementClient "github.com/rancher/rancher/pkg/client/generated/management/v3"
)

func TestProvisioningStateWriteErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		keys    []string
		fields  func() map[string]*schema.Schema
		flatten func(*schema.ResourceData) error
	}{
		{
			"machine_config", []string{"kind", "name", "fleet_namespace", "resource_version", "labels"}, machineConfigV2Fields,
			func(d *schema.ResourceData) error {
				obj := &MachineConfigV2{}
				obj.ID, obj.Kind = "response-id", machineConfigV2NutanixKind
				return flattenMachineConfigV2(d, obj)
			},
		},
		{
			"node_driver", []string{"active", "builtin", "checksum", "description", "name", "external_id", "ui_url", "url", "labels"}, nodeDriverFields,
			func(d *schema.ResourceData) error {
				obj := &managementClient.NodeDriver{}
				obj.ID = "response-id"
				return flattenNodeDriver(d, obj)
			},
		},
		{
			"cluster_driver", []string{"active", "labels"}, clusterDriverFields,
			func(d *schema.ResourceData) error {
				obj := &managementClient.KontainerDriver{}
				obj.ID = "response-id"
				return flattenClusterDriver(d, obj)
			},
		},
		{
			"feature", []string{"name", "labels"}, featureFields,
			func(d *schema.ResourceData) error {
				obj := &managementClient.Feature{}
				obj.ID = "response-id"
				return flattenFeature(d, obj)
			},
		},
	} {
		for _, key := range tc.keys {
			t.Run(tc.name+"/"+key, func(t *testing.T) {
				fields := tc.fields()
				delete(fields, key)
				d := schema.TestResourceDataRaw(t, fields, nil)
				d.SetId("prior-id")
				err := tc.flatten(d)
				if err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("state write %q error = %v", key, err)
				}
				if d.Id() != "prior-id" || d.State() == nil || d.State().ID != "prior-id" {
					t.Fatalf("failed flatten replaced prior identity: %q", d.Id())
				}
			})
		}
	}
}
