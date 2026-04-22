package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/baaaaaaaka/claude_code_helper/internal/claudehistory"
	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

type claudeRunJSONSpec struct {
	Cwd                  string   `json:"cwd,omitempty"`
	Args                 []string `json:"args,omitempty"`
	Prompt               *string  `json:"prompt,omitempty"`
	StdinPath            string   `json:"stdinPath,omitempty"`
	StdoutPath           string   `json:"stdoutPath,omitempty"`
	StderrPath           string   `json:"stderrPath,omitempty"`
	Headless             bool     `json:"headless,omitempty"`
	PreserveRetryOutputs bool     `json:"preserveRetryOutputs,omitempty"`
}

type preparedClaudeRunJSONSpec struct {
	SpecPath             string
	SpecDir              string
	Cwd                  string
	Args                 []string
	Prompt               *string
	StdinPath            string
	StdoutPath           string
	StderrPath           string
	Headless             bool
	PreserveRetryOutputs bool
}

func (spec preparedClaudeRunJSONSpec) hasFileRedirection() bool {
	return strings.TrimSpace(spec.StdinPath) != "" ||
		strings.TrimSpace(spec.StdoutPath) != "" ||
		strings.TrimSpace(spec.StderrPath) != ""
}

func (spec preparedClaudeRunJSONSpec) usesCustomIO() bool {
	return spec.Headless || spec.hasFileRedirection()
}

var runClaudeJSONSpecFunc = runClaudeJSONSpec

func newRunJSONCmd(root *rootOptions) *cobra.Command {
	var claudeDir string
	var claudePath string
	var profileRef string

	cmd := &cobra.Command{
		Use:   "run-json <spec.json>",
		Short: "Run Claude using a JSON spec",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := loadClaudeRunJSONSpec(args[0])
			if err != nil {
				return err
			}

			store, err := config.NewStore(root.configPath)
			if err != nil {
				return err
			}

			pref, err := resolveRunJSONProxyPreference(store, profileRef)
			if err != nil {
				return err
			}
			useProxy, cfg := pref.Enabled, pref.Cfg

			var profile *config.Profile
			if useProxy {
				p, cfgWithProfile, err := ensureProfile(cmd.Context(), store, profileRef, false, cmd.OutOrStdout())
				if err != nil {
					return err
				}
				cfg = cfgWithProfile
				profile = &p
			}

			return runClaudeJSONSpecFunc(
				cmd.Context(),
				root,
				store,
				profile,
				cfg.Instances,
				spec,
				claudePath,
				claudeDir,
				useProxy,
				shouldUseRunJSONYoloBypass(cfg),
				cmd.ErrOrStderr(),
			)
		},
	}

	cmd.Flags().StringVar(&claudeDir, "claude-dir", "", "Override Claude Code data dir (default: ~/.claude)")
	cmd.Flags().StringVar(&claudePath, "claude-path", "", explicitClaudePathFlagHelp)
	cmd.Flags().StringVar(&profileRef, "profile", "", "Proxy profile id or name")
	return cmd
}

func resolveRunJSONProxyPreference(store *config.Store, profileRef string) (proxyPreferenceResult, error) {
	cfg, err := store.Load()
	if err != nil {
		return proxyPreferenceResult{Cfg: cfg}, err
	}
	if strings.TrimSpace(profileRef) != "" {
		return proxyPreferenceResult{Enabled: true, Cfg: cfg}, nil
	}
	if cfg.ProxyEnabled != nil {
		return proxyPreferenceResult{Enabled: *cfg.ProxyEnabled, Cfg: cfg}, nil
	}
	if len(cfg.Profiles) > 0 {
		return proxyPreferenceResult{Cfg: cfg}, fmt.Errorf("run-json requires an explicit proxy choice; set a saved proxy preference first or pass --profile")
	}
	return proxyPreferenceResult{Enabled: false, Cfg: cfg}, nil
}

func shouldUseRunJSONYoloBypass(cfg config.Config) bool {
	return resolveYoloVisible(cfg)
}

func loadClaudeRunJSONSpec(specPath string) (preparedClaudeRunJSONSpec, error) {
	absPath, err := filepath.Abs(specPath)
	if err != nil {
		return preparedClaudeRunJSONSpec{}, fmt.Errorf("resolve spec path %q: %w", specPath, err)
	}

	f, err := os.Open(absPath)
	if err != nil {
		return preparedClaudeRunJSONSpec{}, err
	}
	defer func() { _ = f.Close() }()

	var raw claudeRunJSONSpec
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return preparedClaudeRunJSONSpec{}, fmt.Errorf("parse run-json spec %s: %w", absPath, err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return preparedClaudeRunJSONSpec{}, fmt.Errorf("parse run-json spec %s: expected a single JSON object", absPath)
		}
		return preparedClaudeRunJSONSpec{}, fmt.Errorf("parse run-json spec %s: %w", absPath, err)
	}

	return prepareClaudeRunJSONSpec(absPath, raw)
}

func prepareClaudeRunJSONSpec(specPath string, raw claudeRunJSONSpec) (preparedClaudeRunJSONSpec, error) {
	absPath, err := filepath.Abs(specPath)
	if err != nil {
		return preparedClaudeRunJSONSpec{}, fmt.Errorf("resolve spec path %q: %w", specPath, err)
	}
	specDir := filepath.Dir(absPath)

	args := normalizeClaudeRunJSONArgs(raw.Args)

	if strings.TrimSpace(raw.Cwd) == "" {
		return preparedClaudeRunJSONSpec{}, fmt.Errorf("run-json spec requires cwd; use \".\" to run in the spec file directory")
	}
	cwd := resolveClaudeRunJSONPath(specDir, raw.Cwd)
	cwd, err = normalizeWorkingDir(cwd)
	if err != nil {
		return preparedClaudeRunJSONSpec{}, err
	}

	stdinPath := resolveClaudeRunJSONOptionalPath(specDir, raw.StdinPath)
	stdoutPath := resolveClaudeRunJSONOptionalPath(specDir, raw.StdoutPath)
	stderrPath := resolveClaudeRunJSONOptionalPath(specDir, raw.StderrPath)

	if err := validateClaudeRunJSONPaths(absPath, stdinPath, stdoutPath, stderrPath); err != nil {
		return preparedClaudeRunJSONSpec{}, err
	}

	return preparedClaudeRunJSONSpec{
		SpecPath:             absPath,
		SpecDir:              specDir,
		Cwd:                  cwd,
		Args:                 append([]string(nil), args...),
		Prompt:               raw.Prompt,
		StdinPath:            stdinPath,
		StdoutPath:           stdoutPath,
		StderrPath:           stderrPath,
		Headless:             raw.Headless,
		PreserveRetryOutputs: raw.PreserveRetryOutputs,
	}, nil
}

func isClaudeBinaryArg(arg string) bool {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(arg))
	return base == "claude" || base == "claude.exe"
}

func normalizeClaudeRunJSONArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	normalized := append([]string(nil), args...)
	for len(normalized) > 0 && isClaudeBinaryArg(normalized[0]) {
		normalized = normalized[1:]
	}
	return normalized
}

func hasExplicitClaudePermissionArgs(args []string) bool {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		switch {
		case arg == "--dangerously-skip-permissions":
			return true
		case arg == "--allow-dangerously-skip-permissions":
			return true
		case arg == "--permission-mode":
			return true
		case strings.HasPrefix(arg, "--permission-mode="):
			return true
		}
	}
	return false
}

func validateClaudeRunJSONPaths(specPath string, stdinPath string, stdoutPath string, stderrPath string) error {
	if err := validateRunTargetIOPaths(stdinPath, stdoutPath, stderrPath); err != nil {
		return fmt.Errorf("run-json spec %w", err)
	}
	if sameCleanPath(specPath, stdoutPath) || sameCleanPath(specPath, stderrPath) {
		return fmt.Errorf("run-json spec output paths must not overwrite the spec file")
	}
	if stdinPath != "" {
		info, err := os.Stat(stdinPath)
		if err != nil {
			return fmt.Errorf("run-json spec stdinPath %s: %w", stdinPath, err)
		}
		if info.IsDir() {
			return fmt.Errorf("run-json spec stdinPath must be a file: %s", stdinPath)
		}
	}
	for _, candidate := range []struct {
		label string
		path  string
	}{
		{label: "stdoutPath", path: stdoutPath},
		{label: "stderrPath", path: stderrPath},
	} {
		if candidate.path == "" {
			continue
		}
		if info, err := os.Stat(candidate.path); err == nil && info.IsDir() {
			return fmt.Errorf("run-json spec %s must be a file path: %s", candidate.label, candidate.path)
		}
	}
	return nil
}

func resolveClaudeRunJSONPath(baseDir string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(baseDir, value))
}

func resolveClaudeRunJSONOptionalPath(baseDir string, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return resolveClaudeRunJSONPath(baseDir, value)
}

func runClaudeJSONSpec(
	ctx context.Context,
	root *rootOptions,
	store *config.Store,
	profile *config.Profile,
	instances []config.Instance,
	spec preparedClaudeRunJSONSpec,
	claudePath string,
	claudeDir string,
	useProxy bool,
	yoloBypassUnlocked bool,
	log io.Writer,
) error {
	launcherLog := log
	statusWriter := log
	if spec.Headless {
		launcherLog = io.Discard
		statusWriter = io.Discard
		if appendWriter := newAppendFileWriter(spec.StderrPath); appendWriter != nil {
			statusWriter = appendWriter
		}
	}

	if useProxy && profile == nil {
		return fmt.Errorf("proxy mode enabled but no profile configured")
	}

	claudePathResolved, err := ensureClaudeInstalled(ctx, claudePath, launcherLog, installProxyOptions{
		UseProxy:           useProxy,
		Profile:            profile,
		Instances:          instances,
		LauncherProbePatch: &root.exePatch,
	})
	if err != nil {
		return err
	}
	claudePath = claudePathResolved

	yoloArgs := []string(nil)
	if yoloBypassUnlocked && !hasExplicitClaudePermissionArgs(spec.Args) {
		yoloArgs = resolveYoloBypassArgs(claudePath, root.configPath)
		if len(yoloArgs) == 0 {
			_, _ = fmt.Fprintln(statusWriter, "yolo: this Claude build does not expose bypass flags; running without bypass")
		}
	}

	cmdArgs := []string{claudePath}
	if len(yoloArgs) > 0 {
		cmdArgs = append(cmdArgs, yoloArgs...)
	}
	cmdArgs = append(cmdArgs, spec.Args...)
	if spec.Prompt != nil {
		cmdArgs = append(cmdArgs, *spec.Prompt)
	}

	patchOpts := root.exePatch
	exePatchOutcome, err := maybePatchExecutableCtxFn(ctx, cmdArgs, patchOpts, root.configPath, launcherLog)
	if err != nil {
		return err
	}
	if patchOpts.dryRun && patchOpts.enabled() {
		return nil
	}

	extraEnv := []string{}
	if claudeDir != "" {
		extraEnv = append(extraEnv, claudehistory.EnvClaudeDir+"="+claudeDir)
	}

	opts := runTargetOptions{
		Cwd:         spec.Cwd,
		ExtraEnv:    extraEnv,
		UseProxy:    useProxy,
		PreserveTTY: !spec.usesCustomIO(),
		PrepareIO: newFileRunTargetIOWithOptions(
			spec.StdinPath,
			spec.StdoutPath,
			spec.StderrPath,
			fileRunTargetIOOptions{
				Headless:            spec.Headless,
				ArchiveRetryOutputs: spec.PreserveRetryOutputs,
			},
		),
		StatusWriter: statusWriter,
		YoloEnabled:  len(yoloArgs) > 0,
		OnYoloRetryPrepare: func(nextArgs []string) (*patchOutcome, error) {
			return maybePatchExecutableCtxFn(ctx, nextArgs, patchOpts, root.configPath, launcherLog)
		},
	}
	if useProxy {
		return runWithProfileOptionsFn(ctx, store, *profile, instances, cmdArgs, exePatchOutcome, opts)
	}
	return runTargetWithFallbackWithOptionsFn(ctx, cmdArgs, "", nil, exePatchOutcome, nil, opts)
}
