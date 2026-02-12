package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/baaaaaaaka/claude_code_helper/internal/claudehistory"
	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/tui"
	"github.com/baaaaaaaka/claude_code_helper/internal/update"
)

var (
	selectSession         = tui.SelectSession
	runClaudeSessionFunc  = runClaudeSession
	runClaudeNewSessionFn = runClaudeNewSession
)

const defaultRefreshInterval = 5 * time.Second

func ambiguousSessionError(sessionID string, projects []claudehistory.Project) error {
	matches := claudehistory.FindSessionAliasMatches(projects, sessionID)
	candidates := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, match := range matches {
		canonicalID := strings.TrimSpace(match.Session.SessionID)
		if canonicalID == "" {
			continue
		}
		projectLabel := strings.TrimSpace(match.Project.Path)
		if projectLabel == "" {
			projectLabel = strings.TrimSpace(match.Project.Key)
		}
		label := canonicalID
		if projectLabel != "" {
			label = fmt.Sprintf("%s (project: %s)", canonicalID, projectLabel)
		}
		if seen[label] {
			continue
		}
		seen[label] = true
		candidates = append(candidates, label)
	}
	if len(candidates) == 0 {
		return fmt.Errorf("session %q is ambiguous; use a canonical session id from `claude-proxy history list --pretty`", sessionID)
	}
	sort.Strings(candidates)
	return fmt.Errorf("session %q is ambiguous; candidate canonical sessions: %s. Use `claude-proxy history list --pretty` for details", sessionID, strings.Join(candidates, ", "))
}

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
	var refreshInterval time.Duration
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Browse history in a terminal UI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHistoryTui(cmd, root, *profileRef, *claudeDir, *claudePath, refreshInterval)
		},
	}
	cmd.Flags().DurationVar(&refreshInterval, "refresh-interval", defaultRefreshInterval, "Auto-refresh interval (0 to disable)")
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
			session, ok, ambiguous := claudehistory.FindSessionByIDMatch(projects, sessionID)
			if ambiguous {
				return ambiguousSessionError(sessionID, projects)
			}
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
			useYolo := resolveYoloEnabled(cfg)

			projects, err := claudehistory.DiscoverProjects(*claudeDir)
			if err != nil && len(projects) == 0 {
				return err
			}
			sessionID := args[0]
			session, project, ok, ambiguous := claudehistory.FindSessionWithProjectMatch(projects, sessionID)
			if ambiguous {
				return ambiguousSessionError(sessionID, projects)
			}
			if !ok {
				return fmt.Errorf("session %q not found", sessionID)
			}
			return runClaudeSessionFunc(
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
				useYolo,
				cmd.ErrOrStderr(),
			)
		},
	}
	return cmd
}

func runHistoryTui(cmd *cobra.Command, root *rootOptions, profileRef string, claudeDir string, claudePath string, refreshInterval time.Duration) error {
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
		useYolo := resolveYoloEnabled(cfg)

		var profile *config.Profile
		if useProxy {
			p, cfgWithProfile, err := ensureProfile(ctx, store, profileRef, true, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			cfg = cfgWithProfile
			profile = &p
		}

		resolvedClaudePath, err := ensureClaudeInstalled(ctx, claudePath, cmd.ErrOrStderr(), installProxyOptions{
			UseProxy:  useProxy,
			Profile:   profile,
			Instances: cfg.Instances,
		})
		if err != nil {
			return err
		}
		claudePath = resolvedClaudePath

		defaultCwd, _ := os.Getwd()
		selection, err := selectSession(ctx, tui.Options{
			LoadProjects: func(ctx context.Context) ([]claudehistory.Project, error) {
				return claudehistory.DiscoverProjects(claudeDir)
			},
			Version:         version,
			ProxyEnabled:    useProxy,
			ProxyConfigured: len(cfg.Profiles) > 0,
			YoloEnabled:     useYolo,
			RefreshInterval: refreshInterval,
			DefaultCwd:      defaultCwd,
			PersistYolo: func(enabled bool) error {
				return persistYoloEnabled(store, enabled)
			},
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
		if selection.Cwd != "" {
			return runClaudeNewSessionFn(
				ctx,
				root,
				store,
				profile,
				cfg.Instances,
				selection.Cwd,
				claudePath,
				claudeDir,
				selection.UseProxy,
				selection.UseYolo,
				cmd.ErrOrStderr(),
			)
		}
		return runClaudeSessionFunc(
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
			selection.UseYolo,
			cmd.ErrOrStderr(),
		)
	}
}
