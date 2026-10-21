package rancher2

import (
	"context"
	"fmt"

	"github.com/rancher/norman/clientbase"
)

func (c *Config) catalogClientContext(ctx context.Context, id string) (*clientbase.APIBaseClient, error) {
	if c == nil || id == "" {
		return nil, fmt.Errorf("catalog client requires provider configuration and cluster ID")
	}
	return c.apiClientContext(ctx, id)
}

func initCatalogClientContext(ctx context.Context, opts *clientbase.ClientOpts, id string) (*clientbase.APIBaseClient, error) {
	if err := rancherReadyContext(ctx, opts); err != nil {
		return nil, fmt.Errorf("catalog readiness: %w", err)
	}
	timeout := opts.Timeout
	phase := ctx
	cancel := func() {}
	if timeout > 0 {
		phase, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	opts.URL += "/k8s/clusters/" + id + rancher2CatalogAPIVersion
	var lastErr error
	for {
		if err := phase.Err(); err != nil {
			if lastErr != nil {
				return nil, fmt.Errorf("catalog schema at cluster %q: %w (last error: %w)", id, err, lastErr)
			}
			return nil, fmt.Errorf("catalog schema at cluster %q: %w", id, err)
		}
		cli, err := clientbase.NewAPIClientContext(phase, opts)
		if err == nil {
			return &cli, nil
		}
		closeContextIdle(&cli)
		if !IsServerError(err) && !IsUnknownSchemaType(err) && !IsNotFound(err) && !IsForbidden(err) {
			return nil, err
		}
		lastErr = err
		if timeout <= 0 {
			return nil, fmt.Errorf("catalog schema at cluster %q: %w (last error: %w)", id, context.DeadlineExceeded, err)
		}
		if waitErr := rancherRetryContext(phase); waitErr != nil {
			return nil, fmt.Errorf("catalog schema at cluster %q: %w (last error: %w)", id, waitErr, err)
		}
	}
}

func getMachineConfigV2ByIDContext(ctx context.Context, c *Config, id, kind string) (*MachineConfigV2, error) {
	if c == nil || id == "" {
		return nil, fmt.Errorf("machine config lookup requires provider configuration and ID")
	}
	phase := ctx
	cancel := func() {}
	c.contextMu.Lock()
	timeout := c.Timeout
	c.contextMu.Unlock()
	if timeout > 0 {
		phase, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	var cli *clientbase.APIBaseClient
	defer func() { c.closeStaleContextClient(rancher2DefaultLocalClusterID, cli) }()
	return machineConfigV2ByID(kind, func(apiType string, out any) error {
		var err error
		cli, err = c.catalogClientContext(phase, rancher2DefaultLocalClusterID)
		if err != nil {
			return err
		}
		var lastErr error
		for {
			if err := phase.Err(); err != nil {
				if lastErr != nil {
					return fmt.Errorf("read machine config %q: %w (last error: %w)", id, err, lastErr)
				}
				return fmt.Errorf("read machine config %q: %w", id, err)
			}
			err = cli.Ops.DoByIDContext(phase, apiType, id, out)
			if err == nil {
				return nil
			}
			lookup := &resourceLookupError{cause: err}
			if !IsServerError(err) && !IsUnknownSchemaType(err) {
				return lookup
			}
			lastErr = lookup
			if timeout <= 0 {
				return fmt.Errorf("read machine config %q: %w (last error: %w)", id, context.DeadlineExceeded, lookup)
			}
			if waitErr := rancherRetryContext(phase); waitErr != nil {
				return fmt.Errorf("read machine config %q: %w (last error: %w)", id, waitErr, lookup)
			}
		}
	})
}
