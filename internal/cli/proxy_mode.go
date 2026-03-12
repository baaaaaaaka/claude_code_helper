package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

// proxyPreferenceResult holds the outcome of ensureProxyPreference.
// NeedsPersist is true when the preference was determined fresh (not read from
// disk) and the caller should call persistProxyPreference after the full
// configuration flow succeeds.
type proxyPreferenceResult struct {
	Enabled      bool
	Cfg          config.Config
	NeedsPersist bool
}

func ensureProxyPreference(ctx context.Context, store *config.Store, profileRef string, out io.Writer) (proxyPreferenceResult, error) {
	return ensureProxyPreferenceWithReader(ctx, store, profileRef, out, bufio.NewReader(os.Stdin))
}

func ensureProxyPreferenceWithReader(
	_ context.Context,
	store *config.Store,
	profileRef string,
	out io.Writer,
	reader *bufio.Reader,
) (proxyPreferenceResult, error) {
	cfg, err := store.Load()
	if err != nil {
		return proxyPreferenceResult{Cfg: cfg}, err
	}

	if cfg.ProxyEnabled != nil {
		return proxyPreferenceResult{Enabled: *cfg.ProxyEnabled, Cfg: cfg}, nil
	}

	if len(cfg.Profiles) > 0 {
		enabled := true
		cfg.ProxyEnabled = &enabled
		return proxyPreferenceResult{Enabled: true, Cfg: cfg, NeedsPersist: true}, nil
	}

	if out != nil {
		_, _ = fmt.Fprintln(out, "claude-proxy can route Claude traffic through an SSH tunnel when your network requires it.")
		_, _ = fmt.Fprintln(out, "If you don't need a proxy, choose \"no\" to connect directly.")
	}
	defaultYes := profileRef != ""
	enabled := promptYesNo(reader, "Use SSH proxy for Claude?", defaultYes)
	cfg.ProxyEnabled = &enabled
	return proxyPreferenceResult{Enabled: enabled, Cfg: cfg, NeedsPersist: true}, nil
}

func persistProxyPreference(store *config.Store, enabled bool) error {
	return store.Update(func(cfg *config.Config) error {
		cfg.ProxyEnabled = boolPtr(enabled)
		return nil
	})
}

func boolPtr(v bool) *bool { return &v }
