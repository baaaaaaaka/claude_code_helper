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

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/update"
)

func handleUpdateAndRestart(ctx context.Context, cmd *cobra.Command) error {
	res, err := update.PerformUpdate(ctx, update.UpdateOptions{
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
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := append([]string{exe}, os.Args[1:]...)
	if runtime.GOOS == "windows" {
		c := exec.Command(exe, args[1:]...)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Start(); err != nil {
			return err
		}
		os.Exit(0)
		return nil
	}
	return syscall.Exec(exe, args, os.Environ())
}
