package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/baaaaaaaka/claude_code_helper/internal/claudehistory"
	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

// Test hooks for verifying runner wiring without spawning a real Claude session.
var (
	runWithProfileOptionsFn            = runWithProfileOptions
	runTargetWithFallbackWithOptionsFn = runTargetWithFallbackWithOptions
)

func buildClaudeResumeCommand(
	claudePath string,
	session claudehistory.Session,
	project claudehistory.Project,
	yoloMode config.YoloMode,
) (string, []string, string, error) {
	return buildClaudeResumeCommandWithYoloArgs(claudePath, session, project, yoloMode, nil)
}

func buildClaudeResumeCommandWithYoloArgs(
	claudePath string,
	session claudehistory.Session,
	project claudehistory.Project,
	yoloMode config.YoloMode,
	yoloArgs []string,
) (string, []string, string, error) {
	if session.SessionID == "" {
		return "", nil, "", fmt.Errorf("missing session id")
	}

	cwd := claudehistory.SessionWorkingDir(session, project)
	if cwd == "" {
		return "", nil, "", fmt.Errorf("cannot determine session working directory")
	}
	cwd, err := normalizeWorkingDir(cwd)
	if err != nil {
		return "", nil, "", err
	}

	path := claudePath
	if path == "" {
		var ok bool
		path, ok = findManagedClaudePath(claudeInstallGOOS, "", os.Getenv)
		if !ok {
			return "", nil, "", fmt.Errorf("claude CLI not found in claude-proxy-managed install")
		}
	}

	args := []string{"--resume", session.SessionID}
	if isBypassYoloMode(yoloMode) {
		resolvedYoloArgs := yoloArgs
		if len(resolvedYoloArgs) == 0 {
			resolvedYoloArgs = yoloBypassArgs(path)
		}
		args = append(append([]string{}, resolvedYoloArgs...), args...)
	}
	return path, args, cwd, nil
}

func normalizeWorkingDir(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", fmt.Errorf("missing working directory")
	}
	if !filepath.IsAbs(cwd) {
		cwd, _ = filepath.Abs(cwd)
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		if err != nil {
			return "", fmt.Errorf("working directory not found: %w", err)
		}
		return "", fmt.Errorf("working directory is not a directory: %s", cwd)
	}
	return cwd, nil
}

func validateRulesModeLaunch(claudePath string, patchOpts exePatchOptions) error {
	if !patchOpts.enabledFlag {
		return fmt.Errorf("YOLO rule mode requires --exe-patch-enabled")
	}
	if !patchOpts.policySettings {
		return fmt.Errorf("YOLO rule mode requires --exe-patch-policy-settings")
	}
	builtinSpecs, err := patchOpts.compileBuiltinSpecs()
	if err != nil {
		return err
	}
	if len(builtinSpecs) == 0 {
		return fmt.Errorf("YOLO rule mode requires built-in Claude patch support")
	}
	exePath, err := execLookPathFn(claudePath)
	if err != nil {
		return fmt.Errorf("resolve Claude executable %q: %w", claudePath, err)
	}
	resolvedPath, err := resolveExecutablePathFn(exePath)
	if err != nil {
		return err
	}
	if !isClaudeExecutable(claudePath, resolvedPath) {
		return fmt.Errorf("YOLO rule mode requires the Claude executable; wrappers or renamed binaries are unsupported: %s", claudePath)
	}
	return nil
}

func validateRulesModePatchActive(outcome *patchOutcome) error {
	if outcome != nil && outcome.BuiltInClaudePatchActive {
		return nil
	}
	return fmt.Errorf("YOLO rule mode requires an active built-in Claude patch")
}

func runClaudeSession(
	ctx context.Context,
	root *rootOptions,
	store *config.Store,
	profile *config.Profile,
	instances []config.Instance,
	session claudehistory.Session,
	project claudehistory.Project,
	claudePath string,
	claudeDir string,
	useProxy bool,
	yoloMode config.YoloMode,
	log io.Writer,
) error {
	claudePathResolved, err := ensureClaudeInstalled(ctx, claudePath, log, installProxyOptions{
		UseProxy:  useProxy,
		Profile:   profile,
		Instances: instances,
	})
	if err != nil {
		return err
	}
	claudePath = claudePathResolved

	yoloArgs := []string(nil)
	if isBypassYoloMode(yoloMode) {
		yoloArgs = resolveYoloBypassArgs(claudePath, root.configPath)
		if len(yoloArgs) == 0 {
			_, _ = fmt.Fprintln(os.Stderr, "yolo: this Claude build does not expose bypass flags; disabling yolo bypass")
			_ = persistYoloMode(store, config.YoloModeOff)
			yoloMode = config.YoloModeOff
		}
	}
	patchOpts := withYoloModePatchOptions(root.exePatch, yoloMode)
	if normalizeYoloMode(yoloMode) == config.YoloModeRules {
		if err := validateRulesModeLaunch(claudePath, patchOpts); err != nil {
			return err
		}
	}
	path, args, cwd, err := buildClaudeResumeCommandWithYoloArgs(claudePath, session, project, yoloMode, yoloArgs)
	if err != nil {
		return err
	}

	cmdArgs := append([]string{path}, args...)
	exePatchOutcome, err := maybePatchExecutableCtxFn(ctx, cmdArgs, patchOpts, root.configPath, log)
	if err != nil {
		return err
	}
	if patchOpts.dryRun && patchOpts.enabled() {
		return nil
	}
	if normalizeYoloMode(yoloMode) == config.YoloModeRules {
		if err := validateRulesModePatchActive(exePatchOutcome); err != nil {
			return err
		}
	}

	extraEnv := []string{}
	if claudeDir != "" {
		extraEnv = append(extraEnv, claudehistory.EnvClaudeDir+"="+claudeDir)
	}

	opts := runTargetOptions{
		Cwd:         cwd,
		ExtraEnv:    extraEnv,
		UseProxy:    useProxy,
		PreserveTTY: true,
		YoloEnabled: isBypassYoloMode(yoloMode),
		OnYoloFallback: func() error {
			return persistYoloMode(store, config.YoloModeOff)
		},
		OnYoloRetryPrepare: func(nextArgs []string) (*patchOutcome, error) {
			return maybePatchExecutableCtxFn(ctx, nextArgs, patchOpts, root.configPath, log)
		},
	}
	if useProxy {
		if profile == nil {
			return fmt.Errorf("proxy mode enabled but no profile configured")
		}
		return runWithProfileOptionsFn(ctx, store, *profile, instances, cmdArgs, exePatchOutcome, opts)
	}
	return runTargetWithFallbackWithOptionsFn(ctx, cmdArgs, "", nil, exePatchOutcome, nil, opts)
}

func runClaudeNewSession(
	ctx context.Context,
	root *rootOptions,
	store *config.Store,
	profile *config.Profile,
	instances []config.Instance,
	cwd string,
	claudePath string,
	claudeDir string,
	useProxy bool,
	yoloMode config.YoloMode,
	log io.Writer,
) error {
	cwd, err := normalizeWorkingDir(cwd)
	if err != nil {
		return err
	}

	claudePathResolved, err := ensureClaudeInstalled(ctx, claudePath, log, installProxyOptions{
		UseProxy:  useProxy,
		Profile:   profile,
		Instances: instances,
	})
	if err != nil {
		return err
	}
	claudePath = claudePathResolved
	yoloArgs := []string(nil)
	if isBypassYoloMode(yoloMode) {
		yoloArgs = resolveYoloBypassArgs(claudePath, root.configPath)
		if len(yoloArgs) == 0 {
			_, _ = fmt.Fprintln(os.Stderr, "yolo: this Claude build does not expose bypass flags; disabling yolo bypass")
			_ = persistYoloMode(store, config.YoloModeOff)
			yoloMode = config.YoloModeOff
		}
	}
	patchOpts := withYoloModePatchOptions(root.exePatch, yoloMode)
	if normalizeYoloMode(yoloMode) == config.YoloModeRules {
		if err := validateRulesModeLaunch(claudePath, patchOpts); err != nil {
			return err
		}
	}
	cmdArgs := []string{claudePath}
	if isBypassYoloMode(yoloMode) {
		cmdArgs = append(cmdArgs, yoloArgs...)
	}

	exePatchOutcome, err := maybePatchExecutableCtxFn(ctx, cmdArgs, patchOpts, root.configPath, log)
	if err != nil {
		return err
	}
	if patchOpts.dryRun && patchOpts.enabled() {
		return nil
	}
	if normalizeYoloMode(yoloMode) == config.YoloModeRules {
		if err := validateRulesModePatchActive(exePatchOutcome); err != nil {
			return err
		}
	}

	extraEnv := []string{}
	if claudeDir != "" {
		extraEnv = append(extraEnv, claudehistory.EnvClaudeDir+"="+claudeDir)
	}

	opts := runTargetOptions{
		Cwd:         cwd,
		ExtraEnv:    extraEnv,
		UseProxy:    useProxy,
		PreserveTTY: true,
		YoloEnabled: isBypassYoloMode(yoloMode),
		OnYoloFallback: func() error {
			return persistYoloMode(store, config.YoloModeOff)
		},
		OnYoloRetryPrepare: func(nextArgs []string) (*patchOutcome, error) {
			return maybePatchExecutableCtxFn(ctx, nextArgs, patchOpts, root.configPath, log)
		},
	}
	if useProxy {
		if profile == nil {
			return fmt.Errorf("proxy mode enabled but no profile configured")
		}
		return runWithProfileOptionsFn(ctx, store, *profile, instances, cmdArgs, exePatchOutcome, opts)
	}
	return runTargetWithFallbackWithOptionsFn(ctx, cmdArgs, "", nil, exePatchOutcome, nil, opts)
}
