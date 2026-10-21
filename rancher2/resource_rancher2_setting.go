package rancher2

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/rancher/norman/clientbase"
	managementClient "github.com/rancher/rancher/pkg/client/generated/management/v3"
)

func resourceRancher2Setting() *schema.Resource {
	return &schema.Resource{
		CreateWithoutTimeout: func(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
			return diag.FromErr(resourceRancher2SettingCreate(ctx, d, meta))
		},
		ReadWithoutTimeout: func(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
			return diag.FromErr(resourceRancher2SettingRead(ctx, d, meta))
		},
		UpdateWithoutTimeout: func(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
			return diag.FromErr(resourceRancher2SettingUpdate(ctx, d, meta))
		},
		DeleteWithoutTimeout: func(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
			return diag.FromErr(resourceRancher2SettingDelete(ctx, d, meta))
		},
		Importer: &schema.ResourceImporter{
			StateContext: resourceRancher2SettingImport,
		},
		Schema: settingFields(),
	}
}

func resourceRancher2SettingCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) error {
	name, ok := d.Get("name").(string)
	if !ok {
		return fmt.Errorf("invalid setting name type %T", d.Get("name"))
	}
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid setting configuration type %T", meta)
	}
	client, err := config.settingClientContext(ctx)
	if err != nil {
		return err
	}
	defer config.closeStaleSettingClient(client)

	// Settings use their name as ID; an existing setting is updated instead of recreated.
	exist, err := config.settingByIDContext(ctx, name)
	if err == nil {
		d.SetId(exist.ID)
		return resourceRancher2SettingUpdate(ctx, d, meta)
	}
	if !resourceLookupNotFound(ctx, err) {
		return err
	}

	setting, err := expandSetting(d)
	if err != nil {
		return err
	}

	log.Printf("[INFO] Creating Setting %s", setting.Name)

	newSetting := &managementClient.Setting{}
	err = client.Ops.DoCreateContext(ctx, managementClient.SettingType, setting, newSetting)
	if err != nil {
		return err
	}

	d.SetId(newSetting.ID)

	stateConf := &retry.StateChangeConf{
		Pending:    []string{"active"},
		Target:     []string{"active"},
		Refresh:    settingStateRefreshFunc(ctx, config, newSetting.ID),
		Timeout:    10 * time.Minute,
		Delay:      1 * time.Second,
		MinTimeout: 3 * time.Second,
	}
	_, waitErr := stateConf.WaitForStateContext(ctx)
	if waitErr != nil {
		return fmt.Errorf(
			"[ERROR] waiting for setting (%s) to be created: %w", newSetting.ID, waitErr)
	}

	return resourceRancher2SettingRead(ctx, d, meta)
}

func resourceRancher2SettingRead(ctx context.Context, d *schema.ResourceData, meta interface{}) error {
	name := d.Get("name").(string)
	log.Printf("[INFO] Refreshing Rancher2 Setting ID %s", d.Id())

	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid setting configuration type %T", meta)
	}

	setting, err := config.settingByIDContext(ctx, name)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if resourceLookupNotFound(ctx, err) {
			log.Printf("[INFO] Setting ID %s not found.", d.Id())
			d.SetId("")
			return nil
		}
		return err
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("read setting %q: %w", name, err)
	}

	return flattenSetting(d, setting)
}

func resourceRancher2SettingUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) error {
	log.Printf("[INFO] Updating Setting ID %s", d.Id())
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid setting configuration type %T", meta)
	}
	client, err := config.settingClientContext(ctx)
	if err != nil {
		return err
	}
	defer config.closeStaleSettingClient(client)

	setting, err := config.settingByIDContext(ctx, d.Id())
	if err != nil {
		return err
	}

	update := map[string]interface{}{
		"value":       d.Get("value").(string),
		"annotations": toMapString(d.Get("annotations").(map[string]interface{})),
		"labels":      toMapString(d.Get("labels").(map[string]interface{})),
	}

	newSetting := &managementClient.Setting{}
	err = client.Ops.DoUpdateContext(ctx, managementClient.SettingType, &setting.Resource, update, newSetting)
	if err != nil {
		return err
	}

	stateConf := &retry.StateChangeConf{
		Pending:    []string{"active"},
		Target:     []string{"active"},
		Refresh:    settingStateRefreshFunc(ctx, config, newSetting.ID),
		Timeout:    10 * time.Minute,
		Delay:      1 * time.Second,
		MinTimeout: 3 * time.Second,
	}
	_, waitErr := stateConf.WaitForStateContext(ctx)
	if waitErr != nil {
		return fmt.Errorf(
			"[ERROR] waiting for setting (%s) to be updated: %w", newSetting.ID, waitErr)
	}

	return resourceRancher2SettingRead(ctx, d, meta)
}

func resourceRancher2SettingDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) error {
	log.Printf("[INFO] Deleting Setting ID %s", d.Id())
	id := d.Id()
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid setting configuration type %T", meta)
	}
	client, err := config.settingClientContext(ctx)
	if err != nil {
		return err
	}
	defer config.closeStaleSettingClient(client)

	setting, err := config.settingByIDContext(ctx, id)
	if err != nil {
		if resourceLookupNotFound(ctx, err) {
			log.Printf("[INFO] Setting ID %s not found.", id)
			d.SetId("")
			return nil
		}
		return err
	}

	// System settings are reset; only user-created settings are deleted.
	if setting.CreatorID != "" {
		err = client.Ops.DoResourceDeleteContext(ctx, managementClient.SettingType, &setting.Resource)
		if err != nil {
			return fmt.Errorf("remove setting: %w", err)
		}

		log.Printf("[DEBUG] Waiting for setting (%s) to be removed", id)

		stateConf := &retry.StateChangeConf{
			Pending:    []string{"active"},
			Target:     []string{"removed"},
			Refresh:    settingStateRefreshFunc(ctx, config, id),
			Timeout:    10 * time.Minute,
			Delay:      1 * time.Second,
			MinTimeout: 3 * time.Second,
		}

		_, waitErr := stateConf.WaitForStateContext(ctx)
		if waitErr != nil {
			return fmt.Errorf(
				"[ERROR] waiting for setting (%s) to be removed: %w", id, waitErr)
		}
	} else {
		err = d.Set("value", "")
		if err != nil {
			return err
		}

		err = resourceRancher2SettingUpdate(ctx, d, meta)
		if err != nil {
			return err
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	d.SetId("")
	return nil
}

// settingStateRefreshFunc polls the setting with the operation's context.
func settingStateRefreshFunc(ctx context.Context, config *Config, settingID string) retry.StateRefreshFunc {
	return func() (interface{}, string, error) {
		obj, err := config.settingByIDContext(ctx, settingID)
		if err != nil {
			if resourceLookupNotFound(ctx, err) {
				return obj, "removed", nil
			}
			return nil, "", err
		}
		return obj, "active", nil
	}
}

func resourceLookupNotFound(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var lookup *resourceLookupError
	var apiError *clientbase.APIError
	return errors.As(err, &lookup) && errors.As(lookup, &apiError) && apiError.StatusCode == http.StatusNotFound
}
