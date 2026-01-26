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

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
)

func currentProxyVersion() string {
	v := strings.TrimSpace(version)
	if v == "" {
		return "dev"
	}
	return v
}

func isClaudeExecutable(cmdArg string, resolvedPath string) bool {
	base := strings.ToLower(filepath.Base(resolvedPath))
	if base == "" {
		base = strings.ToLower(filepath.Base(cmdArg))
	}
	return base == "claude" || base == "claude.exe"
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
	out, err := runClaudeProbe(path, "--help")
	if strings.Contains(out, "--permission-mode") {
		return true
	}
	if err != nil {
		return true
	}
	lower := strings.ToLower(out)
	if strings.Contains(lower, "usage") || strings.Contains(lower, "claude") {
		return false
	}
	return true
}

func runClaudeProbe(path string, arg string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, arg)
	out, err := cmd.CombinedOutput()
	return string(out), err
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
	return cfg.HasPatchFailure(proxyVersion, claudeVersion, claudeSHA), nil
}

func recordPatchFailure(configPath string, outcome *patchOutcome, reason string) error {
	if outcome == nil || !outcome.IsClaude {
		return nil
	}
	proxyVersion := currentProxyVersion()
	claudeVersion := strings.TrimSpace(outcome.TargetVersion)
	entry := config.PatchFailure{
		ProxyVersion:  proxyVersion,
		ClaudeVersion: claudeVersion,
		ClaudePath:    outcome.TargetPath,
		ClaudeSHA256:  outcome.TargetSHA256,
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

func isYoloFailure(err error, output string) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "permission-mode") {
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
		if cmdArgs[i] == "--permission-mode" {
			if i+1 < len(cmdArgs) && cmdArgs[i+1] == "bypassPermissions" {
				skipNext = true
			}
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
