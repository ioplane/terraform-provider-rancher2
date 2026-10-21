package rancher2

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	managementClient "github.com/rancher/rancher/pkg/client/generated/management/v3"
)

// Flatteners

func flattenClusterDriver(d *schema.ResourceData, in *managementClient.KontainerDriver) error {
	if in == nil {
		return nil
	}

	if err := d.Set("active", in.Active); err != nil {
		return fmt.Errorf("set cluster driver active: %w", err)
	}
	if err := d.Set("actual_url", in.ActualURL); err != nil {
		return fmt.Errorf("set cluster driver actual_url: %w", err)
	}
	if err := d.Set("builtin", in.BuiltIn); err != nil {
		return fmt.Errorf("set cluster driver builtin: %w", err)
	}
	if err := d.Set("checksum", in.Checksum); err != nil {
		return fmt.Errorf("set cluster driver checksum: %w", err)
	}
	if err := d.Set("name", in.Name); err != nil {
		return fmt.Errorf("set cluster driver name: %w", err)
	}
	if err := d.Set("ui_url", in.UIURL); err != nil {
		return fmt.Errorf("set cluster driver ui_url: %w", err)
	}
	if err := d.Set("url", in.URL); err != nil {
		return fmt.Errorf("set cluster driver url: %w", err)
	}

	err := d.Set("whitelist_domains", toArrayInterface(in.WhitelistDomains))
	if err != nil {
		return err
	}

	err = d.Set("annotations", toMapInterface(in.Annotations))
	if err != nil {
		return err
	}

	err = d.Set("labels", toMapInterface(in.Labels))
	if err != nil {
		return err
	}

	d.SetId(in.ID)
	return nil

}

// Expanders

func expandClusterDriver(in *schema.ResourceData) *managementClient.KontainerDriver {
	obj := &managementClient.KontainerDriver{}
	if in == nil {
		return nil
	}

	if v := in.Id(); len(v) > 0 {
		obj.ID = v
	}

	obj.Active = in.Get("active").(bool)
	obj.ActualURL = in.Get("actual_url").(string)
	obj.BuiltIn = in.Get("builtin").(bool)
	obj.Checksum = in.Get("checksum").(string)
	obj.Name = in.Get("name").(string)
	obj.UIURL = in.Get("ui_url").(string)
	obj.URL = in.Get("url").(string)

	if v, ok := in.Get("whitelist_domains").([]interface{}); ok && len(v) > 0 {
		obj.WhitelistDomains = toArrayString(v)
	}

	if v, ok := in.Get("annotations").(map[string]interface{}); ok && len(v) > 0 {
		obj.Annotations = toMapString(v)
	}

	if v, ok := in.Get("labels").(map[string]interface{}); ok && len(v) > 0 {
		obj.Labels = toMapString(v)
	}

	return obj
}
