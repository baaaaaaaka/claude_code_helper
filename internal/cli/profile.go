package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func ensureProfile(
	ctx context.Context,
	store *config.Store,
	profileRef string,
	autoInit bool,
	out io.Writer,
) (config.Profile, config.Config, error) {
	cfg, err := store.Load()
	if err != nil {
		return config.Profile{}, cfg, err
	}

	var created *config.Profile
	if len(cfg.Profiles) == 0 && autoInit {
		p, err := initProfileInteractive(ctx, store)
		if err != nil {
			return config.Profile{}, cfg, err
		}
		if out != nil {
			_, _ = fmt.Fprintf(out, "Saved profile %q (%s)\n", p.Name, p.ID)
		}
		created = &p

		cfg, err = store.Load()
		if err != nil {
			return config.Profile{}, cfg, err
		}
	}

	if created != nil && profileRef == "" {
		return *created, cfg, nil
	}

	p, err := selectProfile(cfg, profileRef)
	if err != nil {
		if created != nil {
			return *created, cfg, nil
		}
		return config.Profile{}, cfg, err
	}
	return p, cfg, nil
}
