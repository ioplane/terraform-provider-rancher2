package rancher2

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceRancher2CloudCredentialsImport(ctx context.Context, d *schema.ResourceData, meta any) ([]*schema.ResourceData, error) {
	cloudCredentialID, driver := splitID(d.Id())
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return nil, fmt.Errorf("invalid cloud credential configuration type %T", meta)
	}
	client, err := config.apiClientContext(ctx, "")
	if err != nil {
		return []*schema.ResourceData{}, err
	}

	defer config.closeStaleContextClient("", client)
	cloudCredential, err := cloudCredentialByIDContext(ctx, client, cloudCredentialID)
	if err != nil {
		return []*schema.ResourceData{}, err
	}

	if err := d.Set("driver", driver); err != nil {
		return nil, fmt.Errorf("import cloud credential driver: %w", err)
	}
	err = flattenCloudCredential(d, cloudCredential)
	if err != nil {
		return []*schema.ResourceData{}, err
	}

	return []*schema.ResourceData{d}, nil
}
