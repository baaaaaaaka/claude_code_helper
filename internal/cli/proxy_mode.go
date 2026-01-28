package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func ensureProxyPreference(ctx context.Context, store *config.Store, profileRef string, out io.Writer) (bool, config.Config, error) {
	return ensureProxyPreferenceWithReader(ctx, store, profileRef, out, bufio.NewReader(os.Stdin))
}

func ensureProxyPreferenceWithReader(
	_ context.Context,
	store *config.Store,
	profileRef string,
	out io.Writer,
	reader *bufio.Reader,
) (bool, config.Config, error) {
	cfg, err := store.Load()
	if err != nil {
		return false, cfg, err
	}

	if cfg.ProxyEnabled != nil {
		return *cfg.ProxyEnabled, cfg, nil
	}

	if len(cfg.Profiles) > 0 {
		enabled := true
		if err := persistProxyPreference(store, enabled); err != nil {
			return false, cfg, err
		}
		cfg.ProxyEnabled = &enabled
		return enabled, cfg, nil
	}

	if out != nil {
		_, _ = fmt.Fprintln(out, "claude-proxy can route Claude traffic through an SSH tunnel when your network requires it.")
		_, _ = fmt.Fprintln(out, "If you don't need a proxy, choose \"no\" to connect directly.")
	}
	defaultYes := profileRef != ""
	enabled := promptYesNo(reader, "Use SSH proxy for Claude?", defaultYes)
	if err := persistProxyPreference(store, enabled); err != nil {
		return false, cfg, err
	}
	cfg.ProxyEnabled = &enabled
	return enabled, cfg, nil
}

func persistProxyPreference(store *config.Store, enabled bool) error {
	return store.Update(func(cfg *config.Config) error {
		cfg.ProxyEnabled = boolPtr(enabled)
		return nil
	})
}

func boolPtr(v bool) *bool { return &v }
