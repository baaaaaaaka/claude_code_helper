package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/claudehistory"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
)

func buildClaudeResumeCommand(
	claudePath string,
	session claudehistory.Session,
	project claudehistory.Project,
	yolo bool,
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
		var err error
		path, err = exec.LookPath("claude")
		if err != nil {
			return "", nil, "", fmt.Errorf("claude CLI not found in PATH")
		}
	}

	args := []string{"--resume", session.SessionID}
	if yolo {
		args = append([]string{"--permission-mode", "bypassPermissions"}, args...)
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
	useYolo bool,
	log io.Writer,
) error {
	claudePathResolved, err := ensureClaudeInstalled(ctx, claudePath, log)
	if err != nil {
		return err
	}
	claudePath = claudePathResolved
	path, args, cwd, err := buildClaudeResumeCommand(claudePath, session, project, useYolo)
	if err != nil {
		return err
	}

	cmdArgs := append([]string{path}, args...)
	patchOutcome, err := maybePatchExecutable(cmdArgs, root.exePatch, root.configPath, log)
	if err != nil {
		return err
	}
	if root.exePatch.dryRun {
		return nil
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
	}
	if useProxy {
		if profile == nil {
			return fmt.Errorf("proxy mode enabled but no profile configured")
		}
		return runWithProfileOptions(ctx, store, *profile, instances, cmdArgs, patchOutcome, opts)
	}
	return runTargetWithFallbackWithOptions(ctx, cmdArgs, "", nil, patchOutcome, nil, opts)
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
	useYolo bool,
	log io.Writer,
) error {
	cwd, err := normalizeWorkingDir(cwd)
	if err != nil {
		return err
	}

	claudePathResolved, err := ensureClaudeInstalled(ctx, claudePath, log)
	if err != nil {
		return err
	}
	claudePath = claudePathResolved
	cmdArgs := []string{claudePath}
	if useYolo {
		cmdArgs = append(cmdArgs, "--permission-mode", "bypassPermissions")
	}

	patchOutcome, err := maybePatchExecutable(cmdArgs, root.exePatch, root.configPath, log)
	if err != nil {
		return err
	}
	if root.exePatch.dryRun {
		return nil
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
	}
	if useProxy {
		if profile == nil {
			return fmt.Errorf("proxy mode enabled but no profile configured")
		}
		return runWithProfileOptions(ctx, store, *profile, instances, cmdArgs, patchOutcome, opts)
	}
	return runTargetWithFallbackWithOptions(ctx, cmdArgs, "", nil, patchOutcome, nil, opts)
}
