package rancher2

import (
	"context"
	"fmt"
	"log"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func dataSourceRancher2Setting() *schema.Resource {
	return &schema.Resource{
		ReadWithoutTimeout: dataSourceRancher2SettingReadContext,

		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
			},
			"value": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func dataSourceRancher2SettingReadContext(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	name := d.Get("name").(string)
	log.Printf("[INFO] Refreshing Rancher2 Setting: %s", name)
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return diag.FromErr(fmt.Errorf("invalid setting configuration type %T", meta))
	}
	setting, err := config.settingByIDContext(ctx, name)
	if err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("value", setting.Value); err != nil {
		return diag.FromErr(fmt.Errorf("set setting %q value: %w", name, err))
	}
	d.SetId(name)
	return nil
}
