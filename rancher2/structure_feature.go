package rancher2

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	managementClient "github.com/rancher/rancher/pkg/client/generated/management/v3"
)

// Flatteners

func flattenFeature(d *schema.ResourceData, in *managementClient.Feature) error {
	if in == nil {
		return fmt.Errorf("[ERROR] flattening feature: Input setting is nil")
	}

	if err := d.Set("name", in.Name); err != nil {
		return fmt.Errorf("set feature name: %w", err)
	}
	if in.Value != nil {
		if err := d.Set("value", *in.Value); err != nil {
			return fmt.Errorf("set feature value: %w", err)
		}
	}

	err := d.Set("annotations", toMapInterface(in.Annotations))
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

func expandFeature(in *schema.ResourceData) (*managementClient.Feature, error) {
	obj := &managementClient.Feature{}
	if in == nil {
		return nil, fmt.Errorf("[ERROR] expanding feature: Input ResourceData is nil")
	}

	if v := in.Id(); len(v) > 0 {
		obj.ID = v
	}

	if v, ok := in.Get("name").(string); ok && len(v) > 0 {
		obj.Name = v
	}
	if v, ok := in.Get("value").(bool); ok {
		obj.Value = &v
	}

	if v, ok := in.Get("annotations").(map[string]interface{}); ok && len(v) > 0 {
		obj.Annotations = toMapString(v)
	}

	if v, ok := in.Get("labels").(map[string]interface{}); ok && len(v) > 0 {
		obj.Labels = toMapString(v)
	}

	return obj, nil
}
