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
	managementClient "github.com/rancher/rancher/pkg/client/generated/management/v3"
)

func resourceRancher2NodeDriver() *schema.Resource {
	return &schema.Resource{
		CreateContext: func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			return diag.FromErr(resourceRancher2NodeDriverCreate(ctx, d, meta))
		},
		ReadContext: func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			return diag.FromErr(resourceRancher2NodeDriverRead(ctx, d, meta))
		},
		UpdateContext: func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			return diag.FromErr(resourceRancher2NodeDriverUpdate(ctx, d, meta))
		},
		DeleteContext: func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			return diag.FromErr(resourceRancher2NodeDriverDelete(ctx, d, meta))
		},
		Importer: &schema.ResourceImporter{
			StateContext: resourceRancher2NodeDriverImport,
		},
		Schema: nodeDriverFields(),
		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(10 * time.Minute),
			Update: schema.DefaultTimeout(10 * time.Minute),
			Delete: schema.DefaultTimeout(10 * time.Minute),
		},
	}
}

func resourceRancher2NodeDriverCreate(ctx context.Context, d *schema.ResourceData, meta any) error {
	nodeDriver := expandNodeDriver(d)

	log.Printf("[INFO] Creating Node Driver %s", nodeDriver.Name)

	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid node driver configuration type %T", meta)
	}
	client, err := config.apiClientContext(ctx, "")
	if err != nil {
		return err
	}

	defer config.closeStaleContextClient("", client)
	newNodeDriver := &managementClient.NodeDriver{}
	err = client.Ops.DoCreateContext(ctx, managementClient.NodeDriverType, nodeDriver, newNodeDriver)
	if newNodeDriver.ID != "" {
		d.SetId(newNodeDriver.ID)
	}
	if err != nil {
		return err
	}
	if newNodeDriver.ID == "" {
		return fmt.Errorf("created node driver response has no ID; reconcile the server before retrying")
	}

	stateConf := &retry.StateChangeConf{
		Pending:    []string{"downloading", "activating"},
		Target:     []string{"active", "inactive"},
		Refresh:    nodeDriverStateRefreshFunc(ctx, client, newNodeDriver.ID),
		Timeout:    d.Timeout(schema.TimeoutCreate),
		Delay:      1 * time.Second,
		MinTimeout: 3 * time.Second,
	}
	_, waitErr := stateConf.WaitForStateContext(ctx)
	if waitErr != nil {
		return fmt.Errorf("[ERROR] waiting for node driver (%s) to be created: %w", newNodeDriver.ID, waitErr)
	}

	return readNodeDriverContext(ctx, d, client)
}

func resourceRancher2NodeDriverRead(ctx context.Context, d *schema.ResourceData, meta any) error {
	log.Printf("[INFO] Refreshing Node Driver ID %s", d.Id())
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid node driver configuration type %T", meta)
	}
	client, err := config.apiClientContext(ctx, "")
	if err != nil {
		return err
	}

	defer config.closeStaleContextClient("", client)
	return readNodeDriverContext(ctx, d, client)
}

func readNodeDriverContext(ctx context.Context, d *schema.ResourceData, client *clientbase.APIBaseClient) error {
	nodeDriver, err := nodeDriverByIDContext(ctx, client, d.Id())
	if err != nil {
		if resourceLookupNotFound(ctx, err) {
			log.Printf("[INFO] Node Driver ID %s not found.", d.Id())
			d.SetId("")
			return nil
		}
		return err
	}
	return flattenNodeDriver(d, nodeDriver)
}

func resourceRancher2NodeDriverUpdate(ctx context.Context, d *schema.ResourceData, meta any) error {
	log.Printf("[INFO] Updating Node Driver ID %s", d.Id())
	id := d.Id()
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid node driver configuration type %T", meta)
	}
	client, err := config.apiClientContext(ctx, "")
	if err != nil {
		return err
	}

	defer config.closeStaleContextClient("", client)
	nodeDriver, err := nodeDriverByIDContext(ctx, client, id)
	if err != nil {
		return err
	}

	update := map[string]interface{}{
		"active":           d.Get("active").(bool),
		"builtin":          d.Get("builtin").(bool),
		"checksum":         d.Get("checksum").(string),
		"description":      d.Get("description").(string),
		"externalId":       d.Get("external_id").(string),
		"name":             d.Get("name").(string),
		"uiUrl":            d.Get("ui_url").(string),
		"url":              d.Get("url").(string),
		"whitelistDomains": toArrayString(d.Get("whitelist_domains").([]interface{})),
		"annotations":      toMapString(d.Get("annotations").(map[string]interface{})),
		"labels":           toMapString(d.Get("labels").(map[string]interface{})),
	}

	newNodeDriver := &managementClient.NodeDriver{}
	err = client.Ops.DoUpdateContext(ctx, managementClient.NodeDriverType, &nodeDriver.Resource, update, newNodeDriver)
	if err != nil {
		return err
	}
	if newNodeDriver.ID != id {
		return fmt.Errorf("updated node driver identity %q does not match %q", newNodeDriver.ID, id)
	}

	stateConf := &retry.StateChangeConf{
		Pending:    []string{"active", "inactive", "downloading", "activating", "deactivating"},
		Target:     []string{"active", "inactive"},
		Refresh:    nodeDriverStateRefreshFunc(ctx, client, id),
		Timeout:    d.Timeout(schema.TimeoutUpdate),
		Delay:      1 * time.Second,
		MinTimeout: 3 * time.Second,
	}
	_, waitErr := stateConf.WaitForStateContext(ctx)
	if waitErr != nil {
		return fmt.Errorf(
			"[ERROR] waiting for node driver (%s) to be updated: %w", id, waitErr)
	}

	return readNodeDriverContext(ctx, d, client)
}

func resourceRancher2NodeDriverDelete(ctx context.Context, d *schema.ResourceData, meta any) error {
	log.Printf("[INFO] Deleting Node Driver ID %s", d.Id())
	id := d.Id()
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid node driver configuration type %T", meta)
	}
	client, err := config.apiClientContext(ctx, "")
	if err != nil {
		return err
	}

	defer config.closeStaleContextClient("", client)
	nodeDriver, err := nodeDriverByIDContext(ctx, client, id)
	if err != nil {
		if resourceLookupNotFound(ctx, err) {
			log.Printf("[INFO] Node Driver ID %s not found.", id)
			d.SetId("")
			return nil
		}
		return err
	}

	if !nodeDriver.Builtin {
		err = client.Ops.DoResourceDeleteContext(ctx, managementClient.NodeDriverType, &nodeDriver.Resource)
		if err != nil {
			return fmt.Errorf("remove node driver: %w", err)
		}

		log.Printf("[DEBUG] Waiting for node driver (%s) to be removed", id)

		stateConf := &retry.StateChangeConf{
			Pending:    []string{"removing"},
			Target:     []string{"removed"},
			Refresh:    nodeDriverStateRefreshFunc(ctx, client, id),
			Timeout:    d.Timeout(schema.TimeoutDelete),
			Delay:      1 * time.Second,
			MinTimeout: 3 * time.Second,
		}

		_, waitErr := stateConf.WaitForStateContext(ctx)
		if waitErr != nil {
			return fmt.Errorf(
				"[ERROR] waiting for node driver (%s) to be removed: %w", id, waitErr)
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	d.SetId("")
	return nil
}

// nodeDriverStateRefreshFunc returns a resource.StateRefreshFunc, used to watch a Rancher NodeDriver.
func nodeDriverStateRefreshFunc(ctx context.Context, client *clientbase.APIBaseClient, nodeDriverID string) retry.StateRefreshFunc {
	return func() (interface{}, string, error) {
		obj, err := nodeDriverByIDContext(ctx, client, nodeDriverID)
		if err != nil {
			if resourceLookupNotFound(ctx, err) {
				return obj, "removed", nil
			}
			return nil, "", err
		}

		return obj, obj.State, nil
	}
}

func nodeDriverByIDContext(ctx context.Context, client *clientbase.APIBaseClient, id string) (*managementClient.NodeDriver, error) {
	if id == "" {
		return nil, fmt.Errorf("node driver lookup requires an ID")
	}
	nodeDriver := &managementClient.NodeDriver{}
	if err := client.Ops.DoByIDContext(ctx, managementClient.NodeDriverType, id, nodeDriver); err != nil {
		return nil, fmt.Errorf("read node driver %q: %w", id, &resourceLookupError{cause: err})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if nodeDriver.ID != id {
		return nil, fmt.Errorf("node driver lookup identity %q does not match %q", nodeDriver.ID, id)
	}
	return nodeDriver, nil
}
