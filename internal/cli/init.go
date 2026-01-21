package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/ids"
)

func newInitCmd(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create an SSH profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := config.NewStore(root.configPath)
			if err != nil {
				return err
			}

			r := bufio.NewReader(os.Stdin)
			name := prompt(r, "Profile name (optional)", "")
			host := promptRequired(r, "SSH host (required)")
			port := promptInt(r, "SSH port", 22)
			user := promptRequired(r, "SSH user (required)")

			if name == "" {
				name = user + "@" + host
			}

			id, err := ids.New()
			if err != nil {
				return err
			}

			prof := config.Profile{
				ID:        id,
				Name:      name,
				Host:      host,
				Port:      port,
				User:      user,
				CreatedAt: time.Now(),
			}

			if err := sshProbe(cmd.Context(), prof, false); err != nil {
				_, _ = fmt.Fprintln(os.Stderr, "Non-interactive SSH probe failed; attempting interactive login...")
				if err2 := sshProbe(cmd.Context(), prof, true); err2 != nil {
					return fmt.Errorf("ssh probe failed: %w", err2)
				}
			}

			if promptYesNo(r, "Generate and install a dedicated SSH key for this profile?", false) {
				keyPath, err := generateKeypair(cmd.Context(), store, prof)
				if err != nil {
					return err
				}
				if err := installPublicKey(cmd.Context(), prof, keyPath+".pub"); err != nil {
					return err
				}
				prof.SSHArgs = []string{"-i", keyPath}

				// Verify key-based login works without prompting.
				if err := sshProbe(cmd.Context(), prof, false); err != nil {
					return fmt.Errorf("key-based ssh probe failed: %w", err)
				}
			}

			if err := store.Update(func(cfg *config.Config) error {
				cfg.UpsertProfile(prof)
				return nil
			}); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved profile %q (%s)\n", prof.Name, prof.ID)
			return nil
		},
	}
	return cmd
}

func prompt(r *bufio.Reader, label, def string) string {
	for {
		if def != "" {
			_, _ = fmt.Fprintf(os.Stderr, "%s [%s]: ", label, def)
		} else {
			_, _ = fmt.Fprintf(os.Stderr, "%s: ", label)
		}
		s, _ := r.ReadString('\n')
		s = strings.TrimSpace(s)
		if s == "" {
			return def
		}
		return s
	}
}

func promptRequired(r *bufio.Reader, label string) string {
	for {
		v := prompt(r, label, "")
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
}

func promptInt(r *bufio.Reader, label string, def int) int {
	for {
		v := prompt(r, label, strconv.Itoa(def))
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil && n > 0 && n <= 65535 {
			return n
		}
	}
}

func promptYesNo(r *bufio.Reader, label string, def bool) bool {
	defStr := "n"
	if def {
		defStr = "y"
	}

	for {
		s := prompt(r, label+" (y/n)", defStr)
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
		}
	}
}

func sshProbe(ctx context.Context, prof config.Profile, interactive bool) error {
	args := []string{
		"-p", strconv.Itoa(prof.Port),
	}
	if !interactive {
		args = append(args,
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=5",
		)
	}
	args = append(args, prof.SSHArgs...)

	dest := prof.User + "@" + prof.Host
	args = append(args, dest, "exit")

	c := exec.CommandContext(ctx, "ssh", args...)
	if interactive {
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}

	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func generateKeypair(ctx context.Context, store *config.Store, prof config.Profile) (string, error) {
	dir := filepath.Dir(store.Path())
	keyDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return "", err
	}

	keyPath := filepath.Join(keyDir, "id_ed25519_"+prof.ID)
	if _, err := os.Stat(keyPath); err == nil {
		return "", fmt.Errorf("key already exists: %s", keyPath)
	}

	args := []string{
		"-t", "ed25519",
		"-f", keyPath,
		"-N", "",
		"-C", "claude-proxy " + prof.Name,
	}
	c := exec.CommandContext(ctx, "ssh-keygen", args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return "", err
	}
	return keyPath, nil
}

func installPublicKey(ctx context.Context, prof config.Profile, pubKeyPath string) error {
	pub, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return err
	}
	if !bytes.HasSuffix(pub, []byte("\n")) {
		pub = append(pub, '\n')
	}

	args := []string{
		"-p", strconv.Itoa(prof.Port),
		prof.User + "@" + prof.Host,
		"umask 077; mkdir -p ~/.ssh; cat >> ~/.ssh/authorized_keys",
	}
	c := exec.CommandContext(ctx, "ssh", args...)
	c.Stdin = bytes.NewReader(pub)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
