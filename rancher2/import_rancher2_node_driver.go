package rancher2

import (
	"context"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceRancher2NodeDriverImport(ctx context.Context, d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	config := meta.(*Config)
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
