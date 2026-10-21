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
		Create: resourceRancher2SettingCreate,
		ReadWithoutTimeout: func(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
			return diag.FromErr(resourceRancher2SettingRead(ctx, d, meta))
		},
		Update: resourceRancher2SettingUpdate,
		Delete: resourceRancher2SettingDelete,
		Importer: &schema.ResourceImporter{
			StateContext: resourceRancher2SettingImport,
		},
		Schema: settingFields(),
	}
}

func resourceRancher2SettingCreate(d *schema.ResourceData, meta interface{}) error {
	client, err := meta.(*Config).ManagementClient()
	if err != nil {
		return err
	}

	// Checking if setting already exist, updating if already exist. setting id = setting name
	exist, err := client.Setting.ByID(d.Get("name").(string))
	if err == nil {
		d.SetId(exist.ID)
		return resourceRancher2SettingUpdate(d, meta)
	}
	if err != nil {
		if !IsNotFound(err) || IsForbidden(err) {
			return err
		}
	}

	setting, err := expandSetting(d)
	if err != nil {
		return err
	}

	log.Printf("[INFO] Creating Setting %s", setting.Name)

	newSetting, err := client.Setting.Create(setting)
	if err != nil {
		return err
	}

	d.SetId(newSetting.ID)

	stateConf := &retry.StateChangeConf{
		Pending:    []string{"active"},
		Target:     []string{"active"},
		Refresh:    settingStateRefreshFunc(client, newSetting.ID),
		Timeout:    10 * time.Minute,
		Delay:      1 * time.Second,
		MinTimeout: 3 * time.Second,
	}
	_, waitErr := stateConf.WaitForState()
	if waitErr != nil {
		return fmt.Errorf(
			"[ERROR] waiting for setting (%s) to be created: %s", newSetting.ID, waitErr)
	}

	return resourceRancher2SettingRead(context.Background(), d, meta)
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
		var lookup *settingLookupError
		var apiError *clientbase.APIError
		if errors.As(err, &lookup) && errors.As(lookup, &apiError) && apiError.StatusCode == http.StatusNotFound {
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

func resourceRancher2SettingUpdate(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[INFO] Updating Setting ID %s", d.Id())
	client, err := meta.(*Config).ManagementClient()
	if err != nil {
		return err
	}

	setting, err := client.Setting.ByID(d.Id())
	if err != nil {
		return err
	}

	update := map[string]interface{}{
		"value":       d.Get("value").(string),
		"annotations": toMapString(d.Get("annotations").(map[string]interface{})),
		"labels":      toMapString(d.Get("labels").(map[string]interface{})),
	}

	newSetting, err := client.Setting.Update(setting, update)
	if err != nil {
		return err
	}

	stateConf := &retry.StateChangeConf{
		Pending:    []string{"active"},
		Target:     []string{"active"},
		Refresh:    settingStateRefreshFunc(client, newSetting.ID),
		Timeout:    10 * time.Minute,
		Delay:      1 * time.Second,
		MinTimeout: 3 * time.Second,
	}
	_, waitErr := stateConf.WaitForState()
	if waitErr != nil {
		return fmt.Errorf(
			"[ERROR] waiting for setting (%s) to be updated: %s", newSetting.ID, waitErr)
	}

	return resourceRancher2SettingRead(context.Background(), d, meta)
}

func resourceRancher2SettingDelete(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[INFO] Deleting Setting ID %s", d.Id())
	id := d.Id()
	client, err := meta.(*Config).ManagementClient()
	if err != nil {
		return err
	}

	setting, err := client.Setting.ByID(id)
	if err != nil {
		if IsNotFound(err) || IsForbidden(err) {
			log.Printf("[INFO] Setting ID %s not found.", id)
			d.SetId("")
			return nil
		}
		return err
	}

	// Deleting setting if it was cretaed by user
	if setting.CreatorID != "" {
		err = client.Setting.Delete(setting)
		if err != nil {
			return fmt.Errorf("Error removing setting: %s", err)
		}

		log.Printf("[DEBUG] Waiting for setting (%s) to be removed", id)

		stateConf := &retry.StateChangeConf{
			Pending:    []string{"active"},
			Target:     []string{"removed"},
			Refresh:    settingStateRefreshFunc(client, id),
			Timeout:    10 * time.Minute,
			Delay:      1 * time.Second,
			MinTimeout: 3 * time.Second,
		}

		_, waitErr := stateConf.WaitForState()
		if waitErr != nil {
			return fmt.Errorf(
				"[ERROR] waiting for setting (%s) to be removed: %s", id, waitErr)
		}
		// Reseting setting to value = "" if it was cretaed by system
	} else {
		err = d.Set("value", "")
		if err != nil {
			return err
		}

		err = resourceRancher2SettingUpdate(d, meta)
		if err != nil {
			return err
		}
	}

	d.SetId("")
	return nil
}

// settingStateRefreshFunc returns a resource.StateRefreshFunc, used to watch a Rancher Project.
func settingStateRefreshFunc(client *managementClient.Client, settingID string) retry.StateRefreshFunc {
	return func() (interface{}, string, error) {
		obj, err := client.Setting.ByID(settingID)
		if err != nil {
			if IsNotFound(err) || IsForbidden(err) {
				return obj, "removed", nil
			}
			return nil, "", err
		}
		return obj, "active", nil
	}
}
