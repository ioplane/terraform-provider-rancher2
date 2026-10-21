package rancher2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rancher/norman/clientbase"
	managementClient "github.com/rancher/rancher/pkg/client/generated/management/v3"
)

type contextInit struct {
	generation uint64
	done       chan struct{}
}

type contextClientState struct {
	client            *clientbase.APIBaseClient
	k8sDefaultVersion string
}

// resourceLookupError distinguishes a resource response from initialization failures.
type resourceLookupError struct {
	cause error
}

func (e *resourceLookupError) Error() string { return e.cause.Error() }
func (e *resourceLookupError) Unwrap() error { return e.cause }

// invalidateContextClientsLocked changes metadata only; detached pools close after unlocking.
func (c *Config) invalidateContextClientsLocked() []*clientbase.APIBaseClient {
	c.contextGeneration++
	old := make([]*clientbase.APIBaseClient, 0, len(c.contextReady))
	for _, ready := range c.contextReady {
		old = append(old, ready.client)
	}
	c.contextReady = nil
	c.contextFlight = nil
	if c.contextChanged != nil {
		close(c.contextChanged)
	}
	c.contextChanged = make(chan struct{})
	return old
}

func closeContextIdle(clients ...*clientbase.APIBaseClient) {
	for _, cli := range clients {
		if cli != nil && cli.Ops != nil && cli.Ops.Client != nil {
			cli.Ops.Client.CloseIdleConnections()
		}
	}
}

func (c *Config) settingClientContext(ctx context.Context) (*clientbase.APIBaseClient, error) {
	return c.apiClientContext(ctx, "")
}

func (c *Config) apiClientContext(ctx context.Context, key string) (*clientbase.APIBaseClient, error) {
	label := "setting"
	if key != "" {
		label = "catalog"
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("acquire %s client: %w", label, err)
		}
		c.contextMu.Lock()
		if err := ctx.Err(); err != nil {
			c.contextMu.Unlock()
			return nil, fmt.Errorf("acquire %s client: %w", label, err)
		}
		if c.contextReady[key] != nil {
			cli := c.contextReady[key].client
			c.contextMu.Unlock()
			return cli, nil
		}
		if c.contextChanged == nil {
			c.contextChanged = make(chan struct{})
		}
		if c.contextFlight[key] != nil {
			done, changed := c.contextFlight[key].done, c.contextChanged
			c.contextMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("wait for %s client: %w", label, ctx.Err())
			case <-done:
			case <-changed:
			}
			continue
		}
		flight := &contextInit{generation: c.contextGeneration, done: make(chan struct{})}
		if c.contextFlight == nil {
			c.contextFlight = make(map[string]*contextInit)
		}
		c.contextFlight[key] = flight
		opts := &clientbase.ClientOpts{URL: c.URL, TokenKey: c.TokenKey, CACerts: c.CACerts, Insecure: c.Insecure, Timeout: c.Timeout}
		seed := c.K8SDefaultVersion
		c.contextMu.Unlock()
		var cli *clientbase.APIBaseClient
		var version string
		var err error
		if key == "" {
			cli, version, err = initSettingClient(ctx, opts, seed)
		} else {
			cli, err = initCatalogClientContext(ctx, opts, key)
		}
		c.contextMu.Lock()
		current := c.contextGeneration == flight.generation && c.contextFlight[key] == flight
		if current {
			delete(c.contextFlight, key)
			if err == nil {
				err = ctx.Err()
			}
			if err == nil {
				if c.contextReady == nil {
					c.contextReady = make(map[string]*contextClientState)
				}
				c.contextReady[key] = &contextClientState{client: cli, k8sDefaultVersion: version}
			}
		}
		close(flight.done)
		c.contextMu.Unlock()
		if !current {
			closeContextIdle(cli)
			if err == nil {
				err = ctx.Err()
			}
			if err != nil {
				return nil, fmt.Errorf("%s client invalidated during initialization: %w", label, err)
			}
			return nil, fmt.Errorf("%s client invalidated during initialization", label)
		}
		if err != nil {
			closeContextIdle(cli)
			return nil, fmt.Errorf("initialize %s client: %w", label, err)
		}
		return cli, nil
	}
}

func (c *Config) settingByIDContext(ctx context.Context, name string) (*managementClient.Setting, error) {
	cli, err := c.settingClientContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("read setting %q: %w", name, err)
	}
	defer c.closeStaleSettingClient(cli)
	setting := &managementClient.Setting{}
	if err := cli.Ops.DoByIDContext(ctx, managementClient.SettingType, name, setting); err != nil {
		return nil, fmt.Errorf("read setting %q: %w", name, &resourceLookupError{cause: err})
	}
	return setting, nil
}

func (c *Config) closeStaleSettingClient(cli *clientbase.APIBaseClient) {
	c.closeStaleContextClient("", cli)
}

func (c *Config) closeStaleContextClient(key string, cli *clientbase.APIBaseClient) {
	c.contextMu.Lock()
	stale := c.contextReady[key] == nil || c.contextReady[key].client != cli
	c.contextMu.Unlock()
	if stale {
		closeContextIdle(cli)
	}
}

func initSettingClient(ctx context.Context, opts *clientbase.ClientOpts, seededDefault string) (*clientbase.APIBaseClient, string, error) {
	if err := rancherReadyContext(ctx, opts); err != nil {
		return nil, "", fmt.Errorf("setting readiness: %w", err)
	}
	timeout := opts.Timeout
	opts.URL += rancher2ClientAPIVersion
	cli, err := clientbase.NewAPIClientContext(ctx, opts)
	if err != nil {
		return nil, "", fmt.Errorf("setting schema: %w", err)
	}
	version, err := k8sDefaultVersionContext(ctx, &cli, timeout, seededDefault)
	if err != nil {
		closeContextIdle(&cli)
		return nil, "", fmt.Errorf("setting default version: %w", err)
	}
	return &cli, version, nil
}

func rancherReadyContext(ctx context.Context, opts *clientbase.ClientOpts) error {
	phase := ctx
	cancel := func() {}
	if opts.Timeout > 0 {
		phase, cancel = context.WithTimeout(ctx, opts.Timeout)
	}
	defer cancel()
	var lastErr error
	for {
		if err := phase.Err(); err != nil {
			if lastErr != nil {
				return fmt.Errorf("rancher is not ready: %w (last error: %w)", err, lastErr)
			}
			return fmt.Errorf("rancher is not ready: %w", err)
		}
		body, err := doGet(phase, opts.URL+"/ping", "", "", "", opts.CACerts, opts.Insecure)
		if err == nil && string(body) == rancher2ReadyAnswer {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("unexpected readiness response %q", body)
		}
		lastErr = err
		if opts.Timeout <= 0 {
			return fmt.Errorf("rancher is not ready: %w (last error: %w)", context.DeadlineExceeded, err)
		}
		if waitErr := rancherRetryContext(phase); waitErr != nil {
			return fmt.Errorf("rancher is not ready: %w (last error: %w)", waitErr, err)
		}
	}
}

func k8sDefaultVersionContext(ctx context.Context, cli *clientbase.APIBaseClient, timeout time.Duration, seed string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("read default version: %w", err)
	}
	if seed != "" {
		return seed, nil
	}
	phase := ctx
	cancel := func() {}
	if timeout > 0 {
		phase, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	var lastErr error
	for {
		if err := phase.Err(); err != nil {
			if lastErr != nil {
				return "", fmt.Errorf("read default version: %w (last error: %w)", err, lastErr)
			}
			return "", fmt.Errorf("read default version: %w", err)
		}
		setting := &managementClient.Setting{}
		err := cli.Ops.DoByIDContext(phase, managementClient.SettingType, "k8s-version", setting)
		if err == nil {
			return setting.Value, nil
		}
		var apiErr *clientbase.APIError
		if !errors.As(err, &apiErr) || (apiErr.StatusCode != http.StatusInternalServerError && apiErr.StatusCode != http.StatusForbidden) {
			return "", fmt.Errorf("read default version: %w", err)
		}
		lastErr = err
		if timeout <= 0 {
			return "", fmt.Errorf("read default version: %w (last error: %w)", context.DeadlineExceeded, err)
		}
		if waitErr := rancherRetryContext(phase); waitErr != nil {
			return "", fmt.Errorf("read default version: %w (last error: %w)", waitErr, err)
		}
	}
}

func rancherRetryContext(ctx context.Context) error {
	timer := time.NewTimer(rancher2RetriesWait * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
