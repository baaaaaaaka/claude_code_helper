package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/claudehistory"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/tui"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/update"
)

func newHistoryCmd(root *rootOptions) *cobra.Command {
	var claudeDir string
	var claudePath string
	var profileRef string

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Inspect Claude Code history",
	}
	cmd.PersistentFlags().StringVar(&claudeDir, "claude-dir", "", "Override Claude Code data dir (default: ~/.claude)")
	cmd.PersistentFlags().StringVar(&claudePath, "claude-path", "", "Override Claude CLI path (default: search PATH)")
	cmd.PersistentFlags().StringVar(&profileRef, "profile", "", "Proxy profile id or name")

	cmd.AddCommand(
		newHistoryTuiCmd(root, &claudeDir, &claudePath, &profileRef),
		newHistoryListCmd(&claudeDir),
		newHistoryShowCmd(&claudeDir),
		newHistoryOpenCmd(root, &claudeDir, &claudePath, &profileRef),
	)
	return cmd
}

func newHistoryTuiCmd(root *rootOptions, claudeDir *string, claudePath *string, profileRef *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Browse history in a terminal UI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHistoryTui(cmd, root, *profileRef, *claudeDir, *claudePath)
		},
	}
	return cmd
}

func newHistoryListCmd(claudeDir *string) *cobra.Command {
	var pretty bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List discovered projects and sessions as JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			projects, err := claudehistory.DiscoverProjects(*claudeDir)
			if err != nil && len(projects) == 0 {
				return err
			}
			payload := map[string]any{"projects": projects}
			out, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				return err
			}
			if !pretty {
				out, err = json.Marshal(payload)
				if err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Pretty-print JSON")
	return cmd
}

func newHistoryShowCmd(claudeDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <session-id>",
		Short: "Print full history for a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := claudehistory.DiscoverProjects(*claudeDir)
			if err != nil && len(projects) == 0 {
				return err
			}
			sessionID := args[0]
			session, ok := claudehistory.FindSessionByID(projects, sessionID)
			if !ok {
				return fmt.Errorf("session %q not found", sessionID)
			}
			messages, err := claudehistory.ReadSessionMessages(session.FilePath, 0)
			if err != nil {
				return err
			}
			txt := claudehistory.FormatSession(session, messages)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), txt)
			return nil
		},
	}
	return cmd
}

func newHistoryOpenCmd(root *rootOptions, claudeDir *string, claudePath *string, profileRef *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "open <session-id>",
		Short: "Open a session in Claude Code",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := config.NewStore(root.configPath)
			if err != nil {
				return err
			}

			useProxy, cfg, err := ensureProxyPreference(cmd.Context(), store, *profileRef, cmd.ErrOrStderr())
			if err != nil {
				return err
			}

			var profile *config.Profile
			if useProxy {
				p, cfgWithProfile, err := ensureProfile(cmd.Context(), store, *profileRef, true, cmd.OutOrStdout())
				if err != nil {
					return err
				}
				cfg = cfgWithProfile
				profile = &p
			}

			projects, err := claudehistory.DiscoverProjects(*claudeDir)
			if err != nil && len(projects) == 0 {
				return err
			}
			sessionID := args[0]
			session, project, ok := claudehistory.FindSessionWithProject(projects, sessionID)
			if !ok {
				return fmt.Errorf("session %q not found", sessionID)
			}
			return runClaudeSession(
				cmd.Context(),
				root,
				store,
				profile,
				cfg.Instances,
				session,
				project,
				*claudePath,
				*claudeDir,
				useProxy,
				cmd.ErrOrStderr(),
			)
		},
	}
	return cmd
}

func runHistoryTui(cmd *cobra.Command, root *rootOptions, profileRef string, claudeDir string, claudePath string) error {
	ctx := cmd.Context()
	store, err := config.NewStore(root.configPath)
	if err != nil {
		return err
	}

	for {
		useProxy, cfg, err := ensureProxyPreference(ctx, store, profileRef, cmd.ErrOrStderr())
		if err != nil {
			return err
		}

		var profile *config.Profile
		if useProxy {
			p, cfgWithProfile, err := ensureProfile(ctx, store, profileRef, true, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			cfg = cfgWithProfile
			profile = &p
		}

		resolvedClaudePath, err := ensureClaudeInstalled(ctx, claudePath, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		claudePath = resolvedClaudePath

		selection, err := tui.SelectSession(ctx, tui.Options{
			LoadProjects: func(ctx context.Context) ([]claudehistory.Project, error) {
				return claudehistory.DiscoverProjects(claudeDir)
			},
			Version:         version,
			ProxyEnabled:    useProxy,
			ProxyConfigured: len(cfg.Profiles) > 0,
			CheckUpdate: func(ctx context.Context) update.Status {
				return update.CheckForUpdate(ctx, update.CheckOptions{
					InstalledVersion: version,
					Timeout:          8 * time.Second,
				})
			},
		})
		if err != nil {
			var upd tui.UpdateRequested
			if errors.As(err, &upd) {
				return handleUpdateAndRestart(ctx, cmd)
			}
			var toggle tui.ProxyToggleRequested
			if errors.As(err, &toggle) {
				if err := persistProxyPreference(store, toggle.Enable); err != nil {
					return err
				}
				if toggle.Enable && toggle.RequireConfig {
					if _, err := initProfileInteractive(ctx, store); err != nil {
						return err
					}
				}
				continue
			}
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if selection == nil {
			return nil
		}
		return runClaudeSession(
			ctx,
			root,
			store,
			profile,
			cfg.Instances,
			selection.Session,
			selection.Project,
			claudePath,
			claudeDir,
			selection.UseProxy,
			cmd.ErrOrStderr(),
		)
	}
}
