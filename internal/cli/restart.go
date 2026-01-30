package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/baaaaaaaka/claude_code_helper/internal/update"
)

var (
	performUpdate  = update.PerformUpdate
	executablePath = os.Executable
	execSelf       = syscall.Exec
	exitFunc       = os.Exit
	startSelf      = startRestartProcess
)

func handleUpdateAndRestart(ctx context.Context, cmd *cobra.Command) error {
	res, err := performUpdate(ctx, update.UpdateOptions{
		Repo:        "",
		Version:     "latest",
		InstallPath: "",
		Timeout:     120 * time.Second,
	})
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Upgrade failed: %v\n", err)
		return err
	}

	if res.RestartRequired {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Update scheduled for v%s. Please restart `claude-proxy`.\n", res.Version)
		return nil
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated to v%s. Restarting...\n", res.Version)
	return restartSelf()
}

func restartSelf() error {
	exe, err := executablePath()
	if err != nil {
		return err
	}
	args := append([]string{exe}, os.Args[1:]...)
	if runtime.GOOS == "windows" {
		if err := startSelf(exe, args[1:]); err != nil {
			return err
		}
		exitFunc(0)
		return nil
	}
	return execSelf(exe, args, os.Environ())
}

func startRestartProcess(exe string, args []string) error {
	c := exec.Command(exe, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Start()
}
