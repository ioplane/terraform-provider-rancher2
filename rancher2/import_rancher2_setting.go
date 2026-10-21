package rancher2

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceRancher2SettingImport(ctx context.Context, d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return nil, fmt.Errorf("invalid setting configuration type %T", meta)
	}
	setting, err := config.settingByIDContext(ctx, d.Id())
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("import setting %q: %w", d.Id(), err)
	}
	if err := flattenSetting(d, setting); err != nil {
		return nil, err
	}

	return []*schema.ResourceData{d}, nil
}
