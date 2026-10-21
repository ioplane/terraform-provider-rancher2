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

type settingInit struct {
	generation uint64
	done       chan struct{}
}

type settingClientState struct {
	client            *clientbase.APIBaseClient
	k8sDefaultVersion string
}

// settingLookupError distinguishes a resource response from initialization failures.
type settingLookupError struct {
	cause error
}

func (e *settingLookupError) Error() string { return e.cause.Error() }
func (e *settingLookupError) Unwrap() error { return e.cause }

// invalidateSettingLocked changes metadata only; the caller closes the old pool after unlocking.
func (c *Config) invalidateSettingLocked() *clientbase.APIBaseClient {
	c.settingGeneration++
	var old *clientbase.APIBaseClient
	if c.settingReady != nil {
		old = c.settingReady.client
	}
	c.settingReady = nil
	c.settingFlight = nil
	if c.settingChanged != nil {
		close(c.settingChanged)
	}
	c.settingChanged = make(chan struct{})
	return old
}

func closeSettingIdle(cli *clientbase.APIBaseClient) {
	if cli != nil {
		cli.Ops.Client.CloseIdleConnections()
	}
}

func (c *Config) settingClientContext(ctx context.Context) (*clientbase.APIBaseClient, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("acquire setting client: %w", err)
		}
		c.settingMu.Lock()
		if err := ctx.Err(); err != nil {
			c.settingMu.Unlock()
			return nil, fmt.Errorf("acquire setting client: %w", err)
		}
		if c.settingReady != nil {
			cli := c.settingReady.client
			c.settingMu.Unlock()
			return cli, nil
		}
		if c.settingChanged == nil {
			c.settingChanged = make(chan struct{})
		}
		if c.settingFlight != nil {
			done, changed := c.settingFlight.done, c.settingChanged
			c.settingMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("wait for setting client: %w", ctx.Err())
			case <-done:
			case <-changed:
			}
			continue
		}
		flight := &settingInit{generation: c.settingGeneration, done: make(chan struct{})}
		c.settingFlight = flight
		opts := &clientbase.ClientOpts{URL: c.URL, TokenKey: c.TokenKey, CACerts: c.CACerts, Insecure: c.Insecure, Timeout: c.Timeout}
		seed := c.K8SDefaultVersion
		c.settingMu.Unlock()
		cli, version, err := initSettingClient(ctx, opts, seed)
		c.settingMu.Lock()
		current := c.settingGeneration == flight.generation && c.settingFlight == flight
		if current {
			c.settingFlight = nil
			if err == nil {
				err = ctx.Err()
			}
			if err == nil {
				c.settingReady = &settingClientState{client: cli, k8sDefaultVersion: version}
			}
		}
		close(flight.done)
		c.settingMu.Unlock()
		if !current {
			closeSettingIdle(cli)
			if err == nil {
				err = ctx.Err()
			}
			if err != nil {
				return nil, fmt.Errorf("setting client invalidated during initialization: %w", err)
			}
			return nil, fmt.Errorf("setting client invalidated during initialization")
		}
		if err != nil {
			closeSettingIdle(cli)
			return nil, fmt.Errorf("initialize setting client: %w", err)
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
		return nil, fmt.Errorf("read setting %q: %w", name, &settingLookupError{cause: err})
	}
	return setting, nil
}

func (c *Config) closeStaleSettingClient(cli *clientbase.APIBaseClient) {
	c.settingMu.Lock()
	stale := c.settingReady == nil || c.settingReady.client != cli
	c.settingMu.Unlock()
	if stale {
		closeSettingIdle(cli)
	}
}

func initSettingClient(ctx context.Context, opts *clientbase.ClientOpts, seededDefault string) (*clientbase.APIBaseClient, string, error) {
	if err := settingReadyContext(ctx, opts); err != nil {
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
		closeSettingIdle(&cli)
		return nil, "", fmt.Errorf("setting default version: %w", err)
	}
	return &cli, version, nil
}

func settingReadyContext(ctx context.Context, opts *clientbase.ClientOpts) error {
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
		if waitErr := settingRetryContext(phase); waitErr != nil {
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
		if waitErr := settingRetryContext(phase); waitErr != nil {
			return "", fmt.Errorf("read default version: %w (last error: %w)", waitErr, err)
		}
	}
}

func settingRetryContext(ctx context.Context) error {
	timer := time.NewTimer(rancher2RetriesWait * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
