package rancher2

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceRancher2NodeDriverImport(ctx context.Context, d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return []*schema.ResourceData{}, fmt.Errorf("invalid node driver configuration type %T", meta)
	}
	client, err := config.apiClientContext(ctx, "")
	if err != nil {
		return []*schema.ResourceData{}, err
	}
	defer config.closeStaleContextClient("", client)
	nodeDriver, err := nodeDriverByIDContext(ctx, client, d.Id())
	if err != nil {
		return []*schema.ResourceData{}, err
	}

	err = flattenNodeDriver(d, nodeDriver)
	if err != nil {
		return []*schema.ResourceData{}, err
	}

	return []*schema.ResourceData{d}, nil
}
