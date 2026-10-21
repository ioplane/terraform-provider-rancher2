package rancher2

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/rancher/norman/clientbase"
	norman "github.com/rancher/norman/types"
)

func resourceRancher2MachineConfigV2() *schema.Resource {
	return &schema.Resource{
		CreateContext: func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			return diag.FromErr(resourceRancher2MachineConfigV2Create(ctx, d, meta))
		},
		ReadWithoutTimeout: func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			return diag.FromErr(readMachineConfigV2Context(ctx, d, meta))
		},
		UpdateContext: func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			return diag.FromErr(resourceRancher2MachineConfigV2Update(ctx, d, meta))
		},
		DeleteContext: func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			return diag.FromErr(resourceRancher2MachineConfigV2Delete(ctx, d, meta))
		},
		Schema: machineConfigV2Fields(),
		CustomizeDiff: func(_ context.Context, d *schema.ResourceDiff, _ any) error {
			plan := d.GetRawPlan()
			if plan.IsNull() || !plan.IsKnown() || !plan.GetAttr("nutanix_config").IsWhollyKnown() {
				return nil
			}
			config, ok := d.Get("nutanix_config").([]any)
			if !ok {
				return nil
			}
			return validateNutanixMachineHardware(expandMachineConfigV2Nutanix(config, &MachineConfigV2{}))
		},
		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(3 * time.Minute),
			Update: schema.DefaultTimeout(10 * time.Minute),
			Delete: schema.DefaultTimeout(10 * time.Minute),
		},
	}
}

func resourceRancher2MachineConfigV2Create(ctx context.Context, d *schema.ResourceData, meta any) error {
	name := d.Get("name").(string)
	obj := expandMachineConfigV2(d)

	log.Printf("[INFO] Creating Machine Config V2 %s kind %s", name, obj.TypeMeta.Kind)

	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid machine config configuration type %T", meta)
	}
	client, err := config.catalogClientContext(ctx, rancher2DefaultLocalClusterID)
	if err != nil {
		return err
	}
	defer config.closeStaleContextClient(rancher2DefaultLocalClusterID, client)
	newObj, err := createMachineConfigV2(ctx, client, obj)
	if newObj != nil && newObj.ID != "" {
		d.SetId(newObj.ID)
	}
	if err != nil {
		return err
	}
	if newObj.ID == "" {
		return fmt.Errorf("created machine config response has no ID; reconcile the server before retrying")
	}
	if err := d.Set("kind", obj.TypeMeta.Kind); err != nil {
		return err
	}
	if err := checkNutanixMachineHardwareEcho(obj.NutanixConfig, newObj.NutanixConfig); err != nil {
		return err
	}

	current, err := waitForMachineConfigV2(ctx, d, config, client)
	if err != nil {
		return fmt.Errorf("wait for machine config %q: %w", newObj.ID, err)
	}
	if current.ID != d.Id() {
		return fmt.Errorf("created machine config lookup identity %q does not match %q", current.ID, d.Id())
	}
	if err := checkNutanixMachineHardwareEcho(obj.NutanixConfig, current.NutanixConfig); err != nil {
		return err
	}
	return flattenMachineConfigV2(d, current)
}

func waitForMachineConfigV2(ctx context.Context, d *schema.ResourceData, config *Config, client *clientbase.APIBaseClient) (*MachineConfigV2, error) {
	log.Printf("[INFO] Waiting for state Machine Config V2 %s", d.Id())

	config.contextMu.Lock()
	timeout := config.Timeout
	config.contextMu.Unlock()
	defer func() { config.closeStaleContextClient(rancher2DefaultLocalClusterID, client) }()
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("wait for machine config %q: %w", d.Id(), err)
		}
		kind := d.Get("kind").(string)
		obj, err := machineConfigV2ByID(kind, func(apiType string, out any) error {
			if client == nil {
				var clientErr error
				client, clientErr = config.catalogClientContext(ctx, rancher2DefaultLocalClusterID)
				if clientErr != nil {
					return clientErr
				}
			}
			return machineConfigLookupContext(ctx, client, timeout, d.Id(), apiType, out)
		})
		if err == nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return obj, nil
		}
		log.Printf("[INFO] Retrying on error Refreshing Machine Config V2 %s: %v", d.Id(), err)
		if IsNotAccessibleByID(err) {
			// Refresh RBAC schemas without the legacy RestartClients management request.
			config.contextMu.Lock()
			old := config.invalidateContextClientsLocked()
			config.contextMu.Unlock()
			closeContextIdle(old...)
			config.closeStaleContextClient(rancher2DefaultLocalClusterID, client)
			client = nil
		}

		if waitErr := rancherRetryContext(ctx); waitErr != nil {
			return nil, fmt.Errorf("wait for machine config %q: %w (last error: %w)", d.Id(), waitErr, err)
		}
	}
}

func resourceRancher2MachineConfigV2Read(d *schema.ResourceData, meta interface{}) error {
	return readMachineConfigV2Context(context.Background(), d, meta)
}

func readMachineConfigV2Context(ctx context.Context, d *schema.ResourceData, meta any) error {
	log.Printf("[INFO] Refreshing Machine Config V2 %s", d.Id())

	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid machine config configuration type %T", meta)
	}
	kind := d.Get("kind").(string)
	obj, err := getMachineConfigV2ByIDContext(ctx, config, d.Id(), kind)
	if err != nil {
		if resourceLookupNotFound(ctx, err) {
			log.Printf("[INFO] Machine Config V2 %s not found", d.Id())
			d.SetId("")
			return nil
		}
		return err
	}
	return flattenMachineConfigV2(d, obj)
}

func resourceRancher2MachineConfigV2Update(ctx context.Context, d *schema.ResourceData, meta any) error {
	obj := expandMachineConfigV2(d)
	log.Printf("[INFO] Updating Machine Config V2 %s", d.Id())

	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid machine config configuration type %T", meta)
	}
	id := d.Id()
	if id == "" {
		return fmt.Errorf("machine config update requires an ID")
	}
	client, err := config.catalogClientContext(ctx, rancher2DefaultLocalClusterID)
	if err != nil {
		return err
	}
	defer config.closeStaleContextClient(rancher2DefaultLocalClusterID, client)
	config.contextMu.Lock()
	timeout := config.Timeout
	config.contextMu.Unlock()
	newObj, err := updateMachineConfigV2(ctx, client, timeout, obj)
	if err != nil {
		return err
	}
	if newObj.ID != id {
		return fmt.Errorf("updated machine config identity %q does not match %q", newObj.ID, id)
	}
	if err := checkNutanixMachineHardwareEcho(obj.NutanixConfig, newObj.NutanixConfig); err != nil {
		return err
	}
	current, err := machineConfigV2ByID(obj.TypeMeta.Kind, func(apiType string, out any) error {
		return machineConfigLookupContext(ctx, client, timeout, id, apiType, out)
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("refresh updated machine config %q: %w", id, err)
	}
	if current.ID != id {
		return fmt.Errorf("refreshed machine config identity %q does not match %q", current.ID, id)
	}
	if err := checkNutanixMachineHardwareEcho(obj.NutanixConfig, current.NutanixConfig); err != nil {
		return err
	}
	return flattenMachineConfigV2(d, current)
}

func resourceRancher2MachineConfigV2Delete(ctx context.Context, d *schema.ResourceData, meta any) error {
	name := d.Get("name").(string)
	kind := d.Get("kind").(string)
	log.Printf("[INFO] Deleting Machine Config V2 %s", name)

	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid machine config configuration type %T", meta)
	}
	id := d.Id()
	if id == "" {
		return fmt.Errorf("machine config deletion requires an ID")
	}
	config.contextMu.Lock()
	timeout := config.Timeout
	config.contextMu.Unlock()
	var client *clientbase.APIBaseClient
	defer func() { config.closeStaleContextClient(rancher2DefaultLocalClusterID, client) }()
	lookup := func() (*MachineConfigV2, error) {
		return machineConfigV2ByID(kind, func(apiType string, out any) error {
			if client == nil {
				var err error
				client, err = config.catalogClientContext(ctx, rancher2DefaultLocalClusterID)
				if err != nil {
					return err
				}
			}
			return machineConfigLookupContext(ctx, client, timeout, id, apiType, out)
		})
	}
	obj, err := lookup()
	if err != nil {
		if resourceLookupNotFound(ctx, err) {
			d.SetId("")
			return nil
		}
		return err
	}
	resource := &norman.Resource{ID: obj.ID, Links: obj.Links, Type: obj.Type, Actions: obj.Actions}
	err = client.Ops.DoResourceDeleteContext(ctx, obj.Type, resource)
	if err != nil {
		return fmt.Errorf("delete machine config %q: %w", id, err)
	}
	stateConf := &retry.StateChangeConf{
		Pending: []string{"active"},
		Target:  []string{"removed"},
		Refresh: func() (any, string, error) {
			current, lookupErr := lookup()
			if lookupErr != nil {
				if resourceLookupNotFound(ctx, lookupErr) {
					return current, "removed", nil
				}
				return nil, "", lookupErr
			}
			return current, "active", nil
		},
		Timeout:    d.Timeout(schema.TimeoutDelete),
		Delay:      1 * time.Second,
		MinTimeout: 3 * time.Second,
	}
	_, waitErr := stateConf.WaitForStateContext(ctx)
	if waitErr != nil {
		return fmt.Errorf("wait for machine config %q removal: %w", id, waitErr)
	}
	d.SetId("")
	return nil
}

// machineConfigV2StateRefreshFunc returns a resource.StateRefreshFunc, used to watch a Rancher Machine Config v2.
func machineConfigV2StateRefreshFunc(meta interface{}, objID, kind string) retry.StateRefreshFunc {
	return func() (interface{}, string, error) {
		obj, err := getMachineConfigV2ByID(meta.(*Config), objID, kind)
		if err != nil {
			if IsNotFound(err) {
				return obj, "removed", nil
			}
			return nil, "", err
		}
		return obj, "active", nil
	}
}

// Rancher2 Machine Config V2 API CRUD functions
func createMachineConfigV2(ctx context.Context, client *clientbase.APIBaseClient, obj *MachineConfigV2) (*MachineConfigV2, error) {
	if client == nil {
		return nil, fmt.Errorf("Creating Machine Config V2: Provider config is nil")
	}
	if obj == nil {
		return nil, fmt.Errorf("Creating Machine Config V2: Machine Config V2 is nil")
	}
	var err error
	out := &MachineConfigV2{}
	kind := obj.TypeMeta.Kind
	switch kind {
	case machineConfigV2Amazonec2Kind:
		resp := &MachineConfigV2Amazonec2{}
		err = client.Ops.DoCreateContext(ctx, machineConfigV2Amazonec2APIType, obj.Amazonec2Config, resp)
		out.Amazonec2Config = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2AzureKind:
		resp := &MachineConfigV2Azure{}
		err = client.Ops.DoCreateContext(ctx, machineConfigV2AzureAPIType, obj.AzureConfig, resp)
		out.AzureConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2DigitaloceanKind:
		resp := &MachineConfigV2Digitalocean{}
		err = client.Ops.DoCreateContext(ctx, machineConfigV2DigitaloceanAPIType, obj.DigitaloceanConfig, resp)
		out.DigitaloceanConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2HarvesterKind:
		resp := &MachineConfigV2Harvester{}
		err = client.Ops.DoCreateContext(ctx, machineConfigV2HarvesterAPIType, obj.HarvesterConfig, resp)
		out.HarvesterConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2LinodeKind:
		resp := &MachineConfigV2Linode{}
		err = client.Ops.DoCreateContext(ctx, machineConfigV2LinodeAPIType, obj.LinodeConfig, resp)
		out.LinodeConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2OpenstackKind:
		resp := &MachineConfigV2Openstack{}
		err = client.Ops.DoCreateContext(ctx, machineConfigV2OpenstackAPIType, obj.OpenstackConfig, resp)
		out.OpenstackConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2VmwarevsphereKind:
		resp := &MachineConfigV2Vmwarevsphere{}
		err = client.Ops.DoCreateContext(ctx, machineConfigV2VmwarevsphereAPIType, obj.VmwarevsphereConfig, resp)
		out.VmwarevsphereConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2GoogleGCEKind:
		resp := &MachineConfigV2GoogleGCE{}
		err = client.Ops.DoCreateContext(ctx, machineConfigV2GoogleGCEAPIType, obj.GoogleGCEConfig, resp)
		out.GoogleGCEConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2NutanixKind:
		resp := &MachineConfigV2Nutanix{}
		if err := validateNutanixMachineHardware(obj.NutanixConfig); err != nil {
			return nil, err
		}
		if err := requireNutanixMachineHardware(obj.NutanixConfig, client.Types[machineConfigV2NutanixAPIType], false); err != nil {
			return nil, err
		}
		err = client.Ops.DoCreateContext(ctx, machineConfigV2NutanixAPIType, obj.NutanixConfig, resp)
		out.NutanixConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	default:
		return nil, fmt.Errorf("[ERROR] Unsupported driver on node template: %s", kind)
	}
	if err != nil {
		return out, fmt.Errorf("create machine config V2: %w", err)
	}
	return out, nil
}

func getMachineConfigV2ByID(c *Config, id, kind string) (*MachineConfigV2, error) {
	if c == nil {
		return nil, fmt.Errorf("Getting Machine Config V2: Provider config is nil")
	}
	if len(id) == 0 {
		return nil, fmt.Errorf("Getting Machine Config V2: Machine Config V2 ID is empty")
	}
	return machineConfigV2ByID(kind, func(apiType string, out any) error {
		return c.getObjectV2ByID(rancher2DefaultLocalClusterID, id, apiType, out)
	})
}

func machineConfigV2ByID(kind string, lookup func(string, any) error) (*MachineConfigV2, error) {
	var err error
	out := &MachineConfigV2{}
	switch kind {
	case machineConfigV2Amazonec2Kind:
		resp := &MachineConfigV2Amazonec2{}
		err = lookup(machineConfigV2Amazonec2APIType, resp)
		out.Amazonec2Config = resp
		out.ID = resp.ID
		out.Links = resp.Links
		out.Actions = resp.Actions
		out.Type = resp.Type
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2AzureKind:
		resp := &MachineConfigV2Azure{}
		err = lookup(machineConfigV2AzureAPIType, resp)
		out.AzureConfig = resp
		out.ID = resp.ID
		out.Links = resp.Links
		out.Actions = resp.Actions
		out.Type = resp.Type
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2DigitaloceanKind:
		resp := &MachineConfigV2Digitalocean{}
		err = lookup(machineConfigV2DigitaloceanAPIType, resp)
		out.DigitaloceanConfig = resp
		out.ID = resp.ID
		out.Links = resp.Links
		out.Actions = resp.Actions
		out.Type = resp.Type
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2HarvesterKind:
		resp := &MachineConfigV2Harvester{}
		err = lookup(machineConfigV2HarvesterAPIType, resp)
		out.HarvesterConfig = resp
		out.ID = resp.ID
		out.Links = resp.Links
		out.Actions = resp.Actions
		out.Type = resp.Type
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2LinodeKind:
		resp := &MachineConfigV2Linode{}
		err = lookup(machineConfigV2LinodeAPIType, resp)
		out.LinodeConfig = resp
		out.ID = resp.ID
		out.Links = resp.Links
		out.Actions = resp.Actions
		out.Type = resp.Type
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2OpenstackKind:
		resp := &MachineConfigV2Openstack{}
		err = lookup(machineConfigV2OpenstackAPIType, resp)
		out.OpenstackConfig = resp
		out.ID = resp.ID
		out.Links = resp.Links
		out.Actions = resp.Actions
		out.Type = resp.Type
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2VmwarevsphereKind:
		resp := &MachineConfigV2Vmwarevsphere{}
		err = lookup(machineConfigV2VmwarevsphereAPIType, resp)
		out.VmwarevsphereConfig = resp
		out.ID = resp.ID
		out.Links = resp.Links
		out.Actions = resp.Actions
		out.Type = resp.Type
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2GoogleGCEKind:
		resp := &MachineConfigV2GoogleGCE{}
		err = lookup(machineConfigV2GoogleGCEAPIType, resp)
		out.GoogleGCEConfig = resp
		out.ID = resp.ID
		out.Links = resp.Links
		out.Actions = resp.Actions
		out.Type = resp.Type
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2NutanixKind:
		resp := &MachineConfigV2Nutanix{}
		err = lookup(machineConfigV2NutanixAPIType, resp)
		out.NutanixConfig = resp
		out.ID = resp.ID
		out.Links = resp.Links
		out.Actions = resp.Actions
		out.Type = resp.Type
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	default:
		return nil, fmt.Errorf("[ERROR] Unsupported driver on node template: %s", kind)
	}
	if err != nil {
		if !IsServerError(err) && !IsNotFound(err) && !IsForbidden(err) {
			return nil, fmt.Errorf("get machine config v2: %w", err)
		}
		return nil, err
	}
	return out, nil
}

func updateMachineConfigV2(ctx context.Context, client *clientbase.APIBaseClient, timeout time.Duration, obj *MachineConfigV2) (*MachineConfigV2, error) {
	if client == nil {
		return nil, fmt.Errorf("Updating Machine Config V2: Provider config is nil")
	}
	if obj == nil {
		return nil, fmt.Errorf("Updating Machine Config V2: Machine Config V2 is nil")
	}
	var err error
	out := &MachineConfigV2{}
	kind := obj.TypeMeta.Kind
	update := func(id, apiType string, payload, response any) error {
		resource := &norman.Resource{}
		if err := machineConfigLookupContext(ctx, client, timeout, id, apiType, resource); err != nil {
			return err
		}
		if resource.ID != id {
			return fmt.Errorf("machine config lookup identity %q does not match %q", resource.ID, id)
		}
		return client.Ops.DoUpdateContext(ctx, apiType, resource, payload, response)
	}
	switch kind {
	case machineConfigV2Amazonec2Kind:
		resp := &MachineConfigV2Amazonec2{}
		err = update(obj.ID, machineConfigV2Amazonec2APIType, obj.Amazonec2Config, resp)
		out.Amazonec2Config = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2AzureKind:
		resp := &MachineConfigV2Azure{}
		err = update(obj.ID, machineConfigV2AzureAPIType, obj.AzureConfig, resp)
		out.AzureConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2DigitaloceanKind:
		resp := &MachineConfigV2Digitalocean{}
		err = update(obj.ID, machineConfigV2DigitaloceanAPIType, obj.DigitaloceanConfig, resp)
		out.DigitaloceanConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2HarvesterKind:
		resp := &MachineConfigV2Harvester{}
		err = update(obj.ID, machineConfigV2HarvesterAPIType, obj.HarvesterConfig, resp)
		out.HarvesterConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2LinodeKind:
		resp := &MachineConfigV2Linode{}
		err = update(obj.ID, machineConfigV2LinodeAPIType, obj.LinodeConfig, resp)
		out.LinodeConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2OpenstackKind:
		resp := &MachineConfigV2Openstack{}
		err = update(obj.ID, machineConfigV2OpenstackAPIType, obj.OpenstackConfig, resp)
		out.OpenstackConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2VmwarevsphereKind:
		resp := &MachineConfigV2Vmwarevsphere{}
		err = update(obj.ID, machineConfigV2VmwarevsphereAPIType, obj.VmwarevsphereConfig, resp)
		out.VmwarevsphereConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2GoogleGCEKind:
		resp := &MachineConfigV2GoogleGCE{}
		err = update(obj.ID, machineConfigV2GoogleGCEAPIType, obj.GoogleGCEConfig, resp)
		out.GoogleGCEConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	case machineConfigV2NutanixKind:
		resp := &MachineConfigV2Nutanix{}
		if err := validateNutanixMachineHardware(obj.NutanixConfig); err != nil {
			return nil, err
		}
		if err := requireNutanixMachineHardware(obj.NutanixConfig, client.Types[machineConfigV2NutanixAPIType], true); err != nil {
			return nil, err
		}
		err = update(obj.ID, machineConfigV2NutanixAPIType, obj.NutanixConfig, resp)
		out.NutanixConfig = resp
		out.ID = resp.ID
		out.TypeMeta = resp.TypeMeta
		out.ObjectMeta = resp.ObjectMeta
	default:
		return nil, fmt.Errorf("[ERROR] Unsupported driver on node template: %s", kind)
	}
	if err != nil {
		return nil, fmt.Errorf("update machine config V2: %w", err)
	}
	return out, err
}
