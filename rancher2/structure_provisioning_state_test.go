package rancher2

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	managementClient "github.com/rancher/rancher/pkg/client/generated/management/v3"
)

func TestProvisioningStateWriteErrors(t *testing.T) {
	for _, key := range []string{"kind", "name", "fleet_namespace", "resource_version", "labels"} {
		t.Run("machine_config/"+key, func(t *testing.T) {
			fields := machineConfigV2Fields()
			delete(fields, key)
			d := schema.TestResourceDataRaw(t, fields, nil)
			d.SetId("prior-id")
			obj := &MachineConfigV2{}
			obj.ID = "response-id"
			obj.Kind = machineConfigV2NutanixKind
			err := flattenMachineConfigV2(d, obj)
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("state write %q error = %v", key, err)
			}
			if d.Id() != "prior-id" || d.State() == nil || d.State().ID != "prior-id" {
				t.Fatalf("failed flatten replaced prior identity: %q", d.Id())
			}
		})
	}
	for _, key := range []string{"active", "builtin", "checksum", "description", "name", "external_id", "ui_url", "url", "labels"} {
		t.Run("node_driver/"+key, func(t *testing.T) {
			fields := nodeDriverFields()
			delete(fields, key)
			d := schema.TestResourceDataRaw(t, fields, nil)
			d.SetId("prior-id")
			obj := &managementClient.NodeDriver{}
			obj.ID = "response-id"
			err := flattenNodeDriver(d, obj)
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("state write %q error = %v", key, err)
			}
			if d.Id() != "prior-id" || d.State() == nil || d.State().ID != "prior-id" {
				t.Fatalf("failed flatten replaced prior identity: %q", d.Id())
			}
		})
	}
}
