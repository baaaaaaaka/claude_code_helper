package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

var probeYoloBypassArgsFn = probeYoloBypassArgs

func currentProxyVersion() string {
	v := strings.TrimSpace(version)
	if v == "" {
		return "dev"
	}
	return v
}

func isClaudeExecutable(cmdArg string, resolvedPath string) bool {
	resolvedBase := strings.ToLower(filepath.Base(resolvedPath))
	if resolvedBase == "claude" || resolvedBase == "claude.exe" {
		return true
	}
	cmdBase := strings.ToLower(filepath.Base(cmdArg))
	return cmdBase == "claude" || cmdBase == "claude.exe"
}

func resolveClaudeVersion(path string) string {
	out, err := runClaudeProbe(path, "--version")
	if err != nil {
		return ""
	}
	return extractVersion(out)
}

func extractVersion(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	re := regexp.MustCompile(`\d+\.\d+\.\d+`)
	if m := re.FindString(output); m != "" {
		return m
	}
	re = regexp.MustCompile(`\d+\.\d+`)
	if m := re.FindString(output); m != "" {
		return m
	}
	fields := strings.Fields(output)
	if len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func supportsYoloFlag(path string) bool {
	args, err := probeYoloBypassArgsFn(path)
	return err == nil && len(args) > 0
}

func yoloBypassArgs(path string) []string {
	args, _ := probeYoloBypassArgsFn(path)
	return args
}

func probeYoloBypassArgs(path string) ([]string, error) {
	out, err := runClaudeProbe(path, "--help")
	if err != nil {
		return nil, err
	}
	hasDangerousSkip := strings.Contains(out, "--dangerously-skip-permissions")
	hasPermissionMode := strings.Contains(out, "--permission-mode")

	switch {
	case hasDangerousSkip && hasPermissionMode:
		return []string{"--dangerously-skip-permissions", "--permission-mode", "bypassPermissions"}, nil
	case hasDangerousSkip:
		return []string{"--dangerously-skip-permissions"}, nil
	case hasPermissionMode:
		return []string{"--permission-mode", "bypassPermissions"}, nil
	}
	return nil, nil
}

func resolveYoloBypassArgs(path string, configPath string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	resolvedPath := resolveYoloProbePath(path)
	proxyVersion := currentProxyVersionFn()
	claudeVersion := strings.TrimSpace(resolveClaudeVersionFn(resolvedPath))
	if args, ok := lookupCachedYoloBypassArgs(configPath, proxyVersion, claudeVersion, resolvedPath); ok {
		return append([]string(nil), args...)
	}
	args, err := probeYoloBypassArgsFn(resolvedPath)
	if err == nil && configPath != "" {
		_ = rememberYoloBypassArgs(configPath, proxyVersion, claudeVersion, resolvedPath, args)
	}
	return append([]string(nil), args...)
}

func resolveYoloProbePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	exePath, err := execLookPathFn(path)
	if err != nil {
		return path
	}
	resolvedPath, err := resolveExecutablePathFn(exePath)
	if err != nil {
		return exePath
	}
	resolvedPath = strings.TrimSpace(resolvedPath)
	if resolvedPath == "" {
		return exePath
	}
	return resolvedPath
}

func lookupCachedYoloBypassArgs(configPath string, proxyVersion string, claudeVersion string, claudePath string) ([]string, bool) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return nil, false
	}
	store, err := config.NewStore(configPath)
	if err != nil {
		return nil, false
	}
	cfg, err := store.Load()
	if err != nil {
		return nil, false
	}
	return cfg.FindYoloBypassProbe(proxyVersion, claudeVersion, claudePath)
}

func rememberYoloBypassArgs(configPath string, proxyVersion string, claudeVersion string, claudePath string, args []string) error {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return nil
	}
	store, err := config.NewStore(configPath)
	if err != nil {
		return err
	}
	return store.Update(func(cfg *config.Config) error {
		cfg.PurgeStaleYoloBypassProbes(proxyVersion)
		cfg.UpsertYoloBypassProbe(config.YoloBypassProbe{
			ProxyVersion:  strings.TrimSpace(proxyVersion),
			ClaudeVersion: strings.TrimSpace(claudeVersion),
			ClaudePath:    strings.TrimSpace(claudePath),
			Args:          append([]string(nil), args...),
			ProbedAt:      time.Now(),
		})
		return nil
	})
}

func hasYoloBypassPermissionsArg(cmdArgs []string) bool {
	if len(cmdArgs) <= 1 {
		return false
	}
	for i := 1; i < len(cmdArgs); i++ {
		arg := strings.TrimSpace(cmdArgs[i])
		if arg == "--dangerously-skip-permissions" {
			return true
		}
		if arg == "--permission-mode" {
			if i+1 < len(cmdArgs) && strings.TrimSpace(cmdArgs[i+1]) == "bypassPermissions" {
				return true
			}
			continue
		}
		if strings.HasPrefix(arg, "--permission-mode=") {
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--permission-mode="))
			if value == "bypassPermissions" {
				return true
			}
		}
	}
	return false
}

func runClaudeProbe(path string, arg string) (string, error) {
	return runClaudeProbeWithContext(context.Background(), path, arg, 15*time.Second)
}

func runClaudeProbeArgs(args []string, arg string) (string, error) {
	return runClaudeProbeArgsWithContext(context.Background(), args, arg, 15*time.Second)
}

func runClaudeProbeWithContext(parent context.Context, path string, arg string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, arg)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runClaudeProbeArgsWithContext(parent context.Context, args []string, arg string, timeout time.Duration) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing probe command")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmdArgs := append(append([]string{}, args...), arg)
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runClaudeProbeOutcome(outcome *patchOutcome, fallbackPath string, arg string) (string, error) {
	if probeArgs := probeArgsForOutcome(outcome, arg); len(probeArgs) > 0 {
		if len(probeArgs) == 2 {
			return runClaudeProbeFn(probeArgs[0], probeArgs[1])
		}
		return runClaudeProbeArgs(probeArgs[:len(probeArgs)-1], probeArgs[len(probeArgs)-1])
	}
	return runClaudeProbeFn(fallbackPath, arg)
}

func shouldSkipPatchFailure(configPath string, proxyVersion string, claudeVersion string, claudeSHA string) (bool, error) {
	store, err := config.NewStore(configPath)
	if err != nil {
		return false, err
	}
	cfg, err := store.Load()
	if err != nil {
		return false, err
	}
	// On Windows, probe timeouts caused by Defender scans may record
	// false-positive failures.  When the proxy is upgraded the stale
	// entries should be discarded so patches get a fresh chance.
	if runtimeGOOS == "windows" {
		if cfg.PurgeStalePatchFailures(proxyVersion) {
			if writeErr := store.Save(cfg); writeErr != nil {
				// Non-fatal: the purge is best-effort.
				_ = writeErr
			}
		}
	}
	hostID := resolveHostID()
	return cfg.HasPatchFailure(hostID, proxyVersion, claudeVersion, claudeSHA), nil
}

func purgeStalePatchFailures(configPath string, proxyVersion string) error {
	if runtimeGOOS != "windows" {
		return nil
	}
	store, err := config.NewStore(configPath)
	if err != nil {
		return err
	}
	cfg, err := store.Load()
	if err != nil {
		return err
	}
	if !cfg.PurgeStalePatchFailures(proxyVersion) {
		return nil
	}
	return store.Save(cfg)
}

func recordPatchFailure(configPath string, outcome *patchOutcome, reason string) error {
	if outcome == nil || !outcome.IsClaude {
		return nil
	}
	proxyVersion := currentProxyVersion()
	claudeVersion := strings.TrimSpace(outcome.TargetVersion)
	claudeSHA := resolvePatchFailureClaudeSHA(outcome)
	entry := config.PatchFailure{
		ProxyVersion:  proxyVersion,
		ClaudeVersion: claudeVersion,
		HostID:        resolveHostID(),
		ClaudePath:    firstNonEmpty(outcome.SourcePath, outcome.TargetPath),
		ClaudeSHA256:  claudeSHA,
		FailedAt:      time.Now(),
		Reason:        strings.TrimSpace(reason),
	}
	store, err := config.NewStore(configPath)
	if err != nil {
		return err
	}
	return store.Update(func(cfg *config.Config) error {
		cfg.UpsertPatchFailure(entry)
		return nil
	})
}

func resolvePatchFailureClaudeSHA(outcome *patchOutcome) string {
	if outcome == nil {
		return ""
	}
	if sha := strings.TrimSpace(outcome.SourceSHA256); sha != "" {
		return sha
	}
	sourcePath := strings.TrimSpace(outcome.SourcePath)
	targetPath := strings.TrimSpace(outcome.TargetPath)
	if sha := strings.TrimSpace(outcome.TargetSHA256); sha != "" {
		return sha
	}
	for _, candidate := range []string{sourcePath, targetPath} {
		if candidate == "" {
			continue
		}
		if sha, err := hashFileSHA256Fn(candidate); err == nil && strings.TrimSpace(sha) != "" {
			return strings.TrimSpace(sha)
		}
	}
	return ""
}

func isYoloFailure(err error, output string) bool {
	if err == nil {
		return false
	}
	if looksLikeYoloRuntimeFailure(output) {
		return true
	}
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "permission-mode") &&
		!strings.Contains(lower, "dangerously-skip-permissions") {
		return false
	}
	if strings.Contains(lower, "unknown") || strings.Contains(lower, "unrecognized") {
		return true
	}
	if strings.Contains(lower, "not supported") || strings.Contains(lower, "invalid") {
		return true
	}
	if strings.Contains(lower, "flag provided but not defined") {
		return true
	}
	return false
}

func looksLikeYoloRuntimeFailure(output string) bool {
	lower := strings.ToLower(output)
	if strings.Contains(lower, "tool permission request failed") {
		return true
	}
	if strings.Contains(lower, "stream closed") && strings.Contains(lower, "permission") {
		return true
	}
	return false
}

func stripYoloArgs(cmdArgs []string) []string {
	if len(cmdArgs) == 0 {
		return cmdArgs
	}
	out := make([]string, 0, len(cmdArgs))
	skipNext := false
	for i := 0; i < len(cmdArgs); i++ {
		if skipNext {
			skipNext = false
			continue
		}
		if cmdArgs[i] == "--dangerously-skip-permissions" ||
			cmdArgs[i] == "--allow-dangerously-skip-permissions" {
			continue
		}
		if cmdArgs[i] == "--permission-mode" {
			if i+1 < len(cmdArgs) && cmdArgs[i+1] == "bypassPermissions" {
				skipNext = true
			}
			continue
		}
		if strings.HasPrefix(cmdArgs[i], "--permission-mode=") &&
			strings.TrimPrefix(cmdArgs[i], "--permission-mode=") == "bypassPermissions" {
			continue
		}
		out = append(out, cmdArgs[i])
	}
	return out
}

func isPatchedBinaryStartupFailure(err error, output string) bool {
	if err == nil {
		return false
	}
	if isPatchedBinaryFailure(err, output) {
		return true
	}
	if exitDueToFatalSignal(err) {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return true
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return true
	}
	return false
}

func formatFailureReason(err error, output string) string {
	reason := strings.TrimSpace(output)
	if reason == "" && err != nil {
		reason = err.Error()
	}
	const maxLen = 240
	if len(reason) > maxLen {
		reason = reason[:maxLen] + "..."
	}
	return reason
}

func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func commandArgsForOutcome(outcome *patchOutcome, cmdArgs []string) []string {
	if outcome == nil || len(outcome.LaunchArgsPrefix) == 0 {
		return append([]string{}, cmdArgs...)
	}
	args := make([]string, 0, len(outcome.LaunchArgsPrefix)+max(0, len(cmdArgs)-1))
	args = append(args, outcome.LaunchArgsPrefix...)
	if len(cmdArgs) > 1 {
		args = append(args, cmdArgs[1:]...)
	}
	return args
}

func probeArgsForOutcome(outcome *patchOutcome, arg string) []string {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil
	}
	if outcome != nil && len(outcome.LaunchArgsPrefix) > 0 {
		args := append([]string{}, outcome.LaunchArgsPrefix...)
		args = append(args, arg)
		return args
	}
	path := ""
	if outcome != nil {
		path = strings.TrimSpace(firstNonEmpty(outcome.TargetPath, outcome.SourcePath))
	}
	if path == "" {
		return nil
	}
	return []string{path, arg}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
