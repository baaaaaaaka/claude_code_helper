package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/ids"
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

			prof, err := initProfileInteractive(cmd.Context(), store)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved profile %q (%s)\n", prof.Name, prof.ID)
			return nil
		},
	}
	return cmd
}

type sshOps interface {
	probe(ctx context.Context, prof config.Profile, interactive bool) error
	generateKeypair(ctx context.Context, store *config.Store, prof config.Profile) (string, error)
	installPublicKey(ctx context.Context, prof config.Profile, pubKeyPath string) error
}

type defaultSSHOps struct{}

func (defaultSSHOps) probe(ctx context.Context, prof config.Profile, interactive bool) error {
	return sshProbe(ctx, prof, interactive)
}

func (defaultSSHOps) generateKeypair(ctx context.Context, store *config.Store, prof config.Profile) (string, error) {
	return generateKeypair(ctx, store, prof)
}

func (defaultSSHOps) installPublicKey(ctx context.Context, prof config.Profile, pubKeyPath string) error {
	return installPublicKey(ctx, prof, pubKeyPath)
}

func initProfileInteractive(ctx context.Context, store *config.Store) (config.Profile, error) {
	return initProfileInteractiveWithDeps(ctx, store, bufio.NewReader(os.Stdin), defaultSSHOps{}, os.Stderr)
}

func initProfileInteractiveWithDeps(
	ctx context.Context,
	store *config.Store,
	reader *bufio.Reader,
	ops sshOps,
	out io.Writer,
) (config.Profile, error) {
	if out != nil {
		_, _ = fmt.Fprintln(out, "Proxy mode uses an SSH tunnel to reach Claude through your network.")
		_, _ = fmt.Fprintln(out, "Enter your SSH host, port, and username to establish that tunnel.")
	}

	host := promptRequired(reader, "SSH host (required)")
	port := promptInt(reader, "SSH port", 22)
	user := promptRequired(reader, "SSH user (required)")

	id, err := ids.New()
	if err != nil {
		return config.Profile{}, err
	}

	name := user + "@" + host
	prof := config.Profile{
		ID:        id,
		Name:      name,
		Host:      host,
		Port:      port,
		User:      user,
		CreatedAt: time.Now(),
	}

	if err := ops.probe(ctx, prof, false); err != nil {
		if out != nil {
			_, _ = fmt.Fprintln(out, "Direct SSH access failed; creating a dedicated claude-proxy key and installing it.")
		}
		keyPath, err := ops.generateKeypair(ctx, store, prof)
		if err != nil {
			return config.Profile{}, err
		}
		if err := ops.installPublicKey(ctx, prof, keyPath+".pub"); err != nil {
			return config.Profile{}, err
		}
		prof.SSHArgs = []string{"-i", keyPath}

		if err := ops.probe(ctx, prof, false); err != nil {
			return config.Profile{}, fmt.Errorf("key-based ssh probe failed: %w", err)
		}
	}

	if err := store.Update(func(cfg *config.Config) error {
		cfg.UpsertProfile(prof)
		return nil
	}); err != nil {
		return config.Profile{}, err
	}

	return prof, nil
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

	keyPath, err := nextAvailableKeyPath(filepath.Join(keyDir, "id_ed25519_"+prof.ID))
	if err != nil {
		return "", err
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

func nextAvailableKeyPath(base string) (string, error) {
	path := base
	for i := 0; ; i++ {
		_, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return path, nil
			}
			return "", err
		}
		path = fmt.Sprintf("%s_%d", base, i+1)
	}
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
