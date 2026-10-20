package rancher2

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

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
