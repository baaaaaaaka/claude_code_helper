package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/claudehistory"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
)

func buildClaudeResumeCommand(
	claudePath string,
	session claudehistory.Session,
	project claudehistory.Project,
) (string, []string, string, error) {
	if session.SessionID == "" {
		return "", nil, "", fmt.Errorf("missing session id")
	}

	cwd := claudehistory.SessionWorkingDir(session, project)
	if cwd == "" {
		return "", nil, "", fmt.Errorf("cannot determine session working directory")
	}
	if !filepath.IsAbs(cwd) {
		cwd, _ = filepath.Abs(cwd)
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		if err != nil {
			return "", nil, "", fmt.Errorf("working directory not found: %w", err)
		}
		return "", nil, "", fmt.Errorf("working directory is not a directory: %s", cwd)
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
	return path, args, cwd, nil
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
	log io.Writer,
) error {
	claudePathResolved, err := ensureClaudeInstalled(ctx, claudePath, log)
	if err != nil {
		return err
	}
	claudePath = claudePathResolved
	path, args, cwd, err := buildClaudeResumeCommand(claudePath, session, project)
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
		Cwd:      cwd,
		ExtraEnv: extraEnv,
		UseProxy: useProxy,
	}
	if useProxy {
		if profile == nil {
			return fmt.Errorf("proxy mode enabled but no profile configured")
		}
		return runWithProfileOptions(ctx, store, *profile, instances, cmdArgs, patchOutcome, opts)
	}
	return runTargetWithFallbackWithOptions(ctx, cmdArgs, "", nil, patchOutcome, nil, opts)
}
