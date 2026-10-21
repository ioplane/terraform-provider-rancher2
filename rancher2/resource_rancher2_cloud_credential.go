package rancher2

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/rancher/norman/clientbase"
	managementClient "github.com/rancher/rancher/pkg/client/generated/management/v3"
)

const (
	amazonec2ConfigDriver     = "amazonec2"
	azureConfigDriver         = "azure"
	digitaloceanConfigDriver  = "digitalocean"
	harvesterConfigDriver     = "harvester"
	linodeConfigDriver        = "linode"
	nutanixConfigDriver       = "nutanix"
	openstackConfigDriver     = "openstack"
	vmwarevsphereConfigDriver = "vmwarevsphere"
)

func resourceRancher2CloudCredential() *schema.Resource {
	return &schema.Resource{
		CreateContext: func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			return diag.FromErr(resourceRancher2CloudCredentialCreate(ctx, d, meta))
		},
		ReadContext: func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			return diag.FromErr(resourceRancher2CloudCredentialRead(ctx, d, meta))
		},
		UpdateContext: func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			return diag.FromErr(resourceRancher2CloudCredentialUpdate(ctx, d, meta))
		},
		DeleteContext: func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			return diag.FromErr(resourceRancher2CloudCredentialDelete(ctx, d, meta))
		},
		Importer: &schema.ResourceImporter{
			StateContext: resourceRancher2CloudCredentialsImport,
		},
		Schema: cloudCredentialFields(),
		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(10 * time.Minute),
			Update: schema.DefaultTimeout(10 * time.Minute),
			Delete: schema.DefaultTimeout(10 * time.Minute),
		},
	}
}

func resourceRancher2CloudCredentialCreate(ctx context.Context, d *schema.ResourceData, meta any) error {
	cloudCredential := expandCloudCredential(d)

	log.Printf("[INFO] Creating Cloud Credential %s", cloudCredential.Name)

	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid cloud credential configuration type %T", meta)
	}
	client, err := config.apiClientContext(ctx, "")
	if err != nil {
		return err
	}
	defer config.closeStaleContextClient("", client)

	if nodeDriver, ok := d.Get("driver").(string); ok && nodeDriver != s3ConfigDriver {
		err = activateNodeDriverContext(ctx, client, nodeDriver)
		if err != nil {
			return err
		}
	}

	newCloudCredential := &CloudCredential{}
	err = client.Ops.DoCreateContext(ctx, managementClient.CloudCredentialType, cloudCredential, newCloudCredential)
	if newCloudCredential.ID != "" {
		d.SetId(newCloudCredential.ID)
	}
	if err != nil {
		return err
	}
	if newCloudCredential.ID == "" {
		return fmt.Errorf("created cloud credential response has no ID; reconcile the server before retrying")
	}

	stateConf := &retry.StateChangeConf{
		Pending:    []string{},
		Target:     []string{"active"},
		Refresh:    cloudCredentialStateRefreshFunc(ctx, client, newCloudCredential.ID),
		Timeout:    d.Timeout(schema.TimeoutCreate),
		Delay:      1 * time.Second,
		MinTimeout: 3 * time.Second,
	}
	_, waitErr := stateConf.WaitForStateContext(ctx)
	if waitErr != nil {
		return fmt.Errorf("[ERROR] waiting for cloud credential (%s) to be created: %w", newCloudCredential.ID, waitErr)
	}

	return readCloudCredentialContext(ctx, d, client)
}

func resourceRancher2CloudCredentialRead(ctx context.Context, d *schema.ResourceData, meta any) error {
	log.Printf("[INFO] Refreshing Cloud Credential ID %s", d.Id())
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid cloud credential configuration type %T", meta)
	}
	client, err := config.apiClientContext(ctx, "")
	if err != nil {
		return err
	}

	defer config.closeStaleContextClient("", client)
	return readCloudCredentialContext(ctx, d, client)
}

func readCloudCredentialContext(ctx context.Context, d *schema.ResourceData, client *clientbase.APIBaseClient) error {
	cloudCredential, err := cloudCredentialByIDContext(ctx, client, d.Id())
	if err != nil {
		if resourceLookupNotFound(ctx, err) {
			d.SetId("")
			return nil
		}
		return err
	}
	return flattenCloudCredential(d, cloudCredential)
}

func resourceRancher2CloudCredentialUpdate(ctx context.Context, d *schema.ResourceData, meta any) error {
	log.Printf("[INFO] Updating Cloud Credential ID %s", d.Id())
	id := d.Id()
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid cloud credential configuration type %T", meta)
	}
	client, err := config.apiClientContext(ctx, "")
	if err != nil {
		return err
	}

	defer config.closeStaleContextClient("", client)
	cloudCredential, err := cloudCredentialByIDContext(ctx, client, id)
	if err != nil {
		return err
	}

	update := map[string]interface{}{
		"name":        d.Get("name").(string),
		"description": d.Get("description").(string),
		"annotations": toMapString(d.Get("annotations").(map[string]interface{})),
		"labels":      toMapString(d.Get("labels").(map[string]interface{})),
	}

	driver := d.Get("driver").(string)
	// migrate google cloud credential to node driver format, not GKE.
	if driver == oldGoogleConfigKontainerDriver {
		driver = googleConfigNodeDriver
		if err := d.Set("driver", driver); err != nil {
			return fmt.Errorf("migrate cloud credential driver: %w", err)
		}
	}

	switch driver {
	case amazonec2ConfigDriver:
		update["amazonec2credentialConfig"] = expandCloudCredentialAmazonec2(d.Get("amazonec2_credential_config").([]interface{}))
	case azureConfigDriver:
		update["azurecredentialConfig"] = expandCloudCredentialAzure(d.Get("azure_credential_config").([]interface{}))
	case digitaloceanConfigDriver:
		update["digitaloceancredentialConfig"] = expandCloudCredentialDigitalocean(d.Get("digitalocean_credential_config").([]interface{}))
	case googleConfigNodeDriver:
		update["googlecredentialConfig"] = expandCloudCredentialGoogle(d.Get("google_credential_config").([]interface{}))
	case harvesterConfigDriver:
		update["harvestercredentialConfig"] = expandCloudCredentialHarvester(d.Get("harvester_credential_config").([]interface{}))
	case linodeConfigDriver:
		update["linodecredentialConfig"] = expandCloudCredentialLinode(d.Get("linode_credential_config").([]interface{}))
	case nutanixConfigDriver:
		update["nutanixcredentialConfig"] = expandCloudCredentialNutanix(d.Get("nutanix_credential_config").([]interface{}))
	case openstackConfigDriver:
		update["openstackcredentialConfig"] = expandCloudCredentialOpenstack(d.Get("openstack_credential_config").([]interface{}))
	case s3ConfigDriver:
		update["s3credentialConfig"] = expandCloudCredentialS3(d.Get("s3_credential_config").([]interface{}))
	case vmwarevsphereConfigDriver:
		update["vmwarevspherecredentialConfig"] = expandCloudCredentialVsphere(d.Get("vsphere_credential_config").([]interface{}))
	default:
		return fmt.Errorf("[ERROR] updating cloud credential: Unsupported driver \"%s\"", driver)
	}

	newCloudCredential := &CloudCredential{}
	err = client.Ops.DoUpdateContext(ctx, managementClient.CloudCredentialType, &cloudCredential.Resource, update, newCloudCredential)
	if err != nil {
		return err
	}
	if newCloudCredential.ID != id {
		return fmt.Errorf("updated cloud credential identity %q does not match %q", newCloudCredential.ID, id)
	}

	stateConf := &retry.StateChangeConf{
		Pending:    []string{"active"},
		Target:     []string{"active"},
		Refresh:    cloudCredentialStateRefreshFunc(ctx, client, id),
		Timeout:    d.Timeout(schema.TimeoutUpdate),
		Delay:      1 * time.Second,
		MinTimeout: 3 * time.Second,
	}
	_, waitErr := stateConf.WaitForStateContext(ctx)
	if waitErr != nil {
		return fmt.Errorf(
			"[ERROR] waiting for cloud credential (%s) to be updated: %w", id, waitErr)
	}

	return readCloudCredentialContext(ctx, d, client)
}

func resourceRancher2CloudCredentialDelete(ctx context.Context, d *schema.ResourceData, meta any) error {
	log.Printf("[INFO] Deleting Cloud Credential ID %s", d.Id())
	id := d.Id()
	config, ok := meta.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("invalid cloud credential configuration type %T", meta)
	}
	client, err := config.apiClientContext(ctx, "")
	if err != nil {
		return err
	}

	defer config.closeStaleContextClient("", client)
	cloudCredential, err := cloudCredentialByIDContext(ctx, client, id)
	if err != nil {
		if resourceLookupNotFound(ctx, err) {
			log.Printf("[INFO] Cloud Credential ID %s not found.", id)
			d.SetId("")
			return nil
		}
		return err
	}

	err = client.Ops.DoResourceDeleteContext(ctx, managementClient.CloudCredentialType, &cloudCredential.Resource)
	if err != nil {
		return fmt.Errorf("remove cloud credential: %w", err)
	}

	log.Printf("[DEBUG] Waiting for cloud credential (%s) to be removed", id)

	stateConf := &retry.StateChangeConf{
		Pending:    []string{},
		Target:     []string{"removed"},
		Refresh:    cloudCredentialStateRefreshFunc(ctx, client, id),
		Timeout:    d.Timeout(schema.TimeoutDelete),
		Delay:      1 * time.Second,
		MinTimeout: 3 * time.Second,
	}

	_, waitErr := stateConf.WaitForStateContext(ctx)
	if waitErr != nil {
		return fmt.Errorf("[ERROR] waiting for cloud credential (%s) to be removed: %w", id, waitErr)
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	d.SetId("")
	return nil
}

// cloudCredentialStateRefreshFunc returns a resource.StateRefreshFunc, used to watch a Rancher CloudCredential.
func cloudCredentialStateRefreshFunc(ctx context.Context, client *clientbase.APIBaseClient, credentialID string) retry.StateRefreshFunc {
	return func() (interface{}, string, error) {
		obj, err := cloudCredentialByIDContext(ctx, client, credentialID)
		if err != nil {
			if resourceLookupNotFound(ctx, err) {
				return obj, "removed", nil
			}
			return nil, "", err
		}

		return obj, "active", nil
	}
}

func cloudCredentialByIDContext(ctx context.Context, client *clientbase.APIBaseClient, id string) (*CloudCredential, error) {
	if id == "" {
		return nil, fmt.Errorf("cloud credential lookup requires an ID")
	}
	credential := &CloudCredential{}
	if err := client.Ops.DoByIDContext(ctx, managementClient.CloudCredentialType, id, credential); err != nil {
		return nil, fmt.Errorf("read cloud credential %q: %w", id, &resourceLookupError{cause: err})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if credential.ID != id {
		return nil, fmt.Errorf("cloud credential lookup identity %q does not match %q", credential.ID, id)
	}
	return credential, nil
}

func activateNodeDriverContext(ctx context.Context, client *clientbase.APIBaseClient, id string) error {
	if id == "" {
		return fmt.Errorf("node driver activation requires an ID")
	}
	activated := false
	for {
		driver, err := nodeDriverByIDContext(ctx, client, id)
		if err == nil {
			if driver.State == "active" {
				return nil
			}
			if !activated {
				// An action may have committed even when its response fails; never replay it.
				if err := client.Ops.DoActionContext(ctx, managementClient.NodeDriverType, "activate", &driver.Resource, nil, nil); err != nil {
					return fmt.Errorf("activate node driver %q; reconcile its state before retrying: %w", id, err)
				}
				activated = true
				continue
			}
		} else {
			var apiError *clientbase.APIError
			retryable := errors.As(err, &apiError) && IsServerError(apiError) || IsUnknownSchemaType(err) || resourceLookupNotFound(ctx, err)
			if !retryable {
				return err
			}
		}
		timer := time.NewTimer(rancher2RetriesWait * time.Second)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for node driver %q: %w", id, ctx.Err())
		}
	}
}
