package cli

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/ids"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/manager"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/proc"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/stack"
)

func newProxyCmd(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Manage long-lived proxy instances",
	}

	cmd.AddCommand(
		newProxyStartCmd(root),
		newProxyDaemonCmd(root),
		newProxyListCmd(root),
		newProxyStopCmd(root),
		newProxyPruneCmd(root),
		newProxyDoctorCmd(root),
	)

	return cmd
}

func newProxyStartCmd(root *rootOptions) *cobra.Command {
	var foreground bool

	cmd := &cobra.Command{
		Use:   "start [profile]",
		Short: "Start a long-lived proxy instance (daemon)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := config.NewStore(root.configPath)
			if err != nil {
				return err
			}
			cfg, err := store.Load()
			if err != nil {
				return err
			}

			profileRef := ""
			if len(cmd.Flags().Args()) > 0 {
				profileRef = cmd.Flags().Args()[0]
			}
			profile, err := selectProfile(cfg, profileRef)
			if err != nil {
				return err
			}

			instanceID, err := ids.New()
			if err != nil {
				return err
			}

			now := time.Now()
			inst := config.Instance{
				ID:         instanceID,
				ProfileID:  profile.ID,
				HTTPPort:   0,
				SocksPort:  0,
				DaemonPID:  0,
				StartedAt:  now,
				LastSeenAt: now,
			}
			if err := manager.RecordInstance(store, inst); err != nil {
				return err
			}

			if foreground {
				return runProxyDaemon(cmd.Context(), store, instanceID)
			}

			exe, err := os.Executable()
			if err != nil {
				return err
			}

			args := []string{}
			if root.configPath != "" {
				args = append(args, "--config", root.configPath)
			}
			args = append(args, "proxy", "daemon", "--instance-id", instanceID)

			c := exec.Command(exe, args...)
			c.Stdin = nil

			logPath := filepath.Join(filepath.Dir(store.Path()), "instances", instanceID+".log")
			if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
				return err
			}
			logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			defer logFile.Close()
			c.Stdout = logFile
			c.Stderr = logFile

			if err := c.Start(); err != nil {
				return err
			}

			pid := c.Process.Pid
			_ = store.Update(func(cfg *config.Config) error {
				for i := range cfg.Instances {
					if cfg.Instances[i].ID == instanceID {
						cfg.Instances[i].DaemonPID = pid
						cfg.Instances[i].LastSeenAt = time.Now()
						return nil
					}
				}
				return nil
			})

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Started instance %s (pid %d). Logs: %s\n", instanceID, pid, logPath)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Use `claude-proxy proxy list` to see assigned ports.")
			return nil
		},
	}

	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run in the foreground (do not fork)")
	return cmd
}

func newProxyDaemonCmd(root *rootOptions) *cobra.Command {
	var instanceID string

	cmd := &cobra.Command{
		Use:    "daemon --instance-id <id>",
		Short:  "Run a proxy instance (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := config.NewStore(root.configPath)
			if err != nil {
				return err
			}
			return runProxyDaemon(cmd.Context(), store, instanceID)
		},
	}

	cmd.Flags().StringVar(&instanceID, "instance-id", "", "Instance id")
	_ = cmd.MarkFlagRequired("instance-id")
	return cmd
}

func runProxyDaemon(parentCtx context.Context, store *config.Store, instanceID string) error {
	ctx, stop := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := store.Load()
	if err != nil {
		return err
	}

	var inst config.Instance
	found := false
	for _, it := range cfg.Instances {
		if it.ID == instanceID {
			inst = it
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("instance %q not found in config", instanceID)
	}

	var prof config.Profile
	pfound := false
	for _, p := range cfg.Profiles {
		if p.ID == inst.ProfileID {
			prof = p
			pfound = true
			break
		}
	}
	if !pfound {
		return fmt.Errorf("profile %q not found for instance %q", inst.ProfileID, instanceID)
	}

	opts := stack.Options{
		SocksPort: inst.SocksPort,
	}
	if inst.HTTPPort > 0 {
		opts.HTTPListenAddr = fmt.Sprintf("127.0.0.1:%d", inst.HTTPPort)
	}

	st, err := stack.Start(prof, instanceID, opts)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close(context.Background()) }()

	now := time.Now()
	inst.DaemonPID = os.Getpid()
	inst.SocksPort = st.SocksPort
	inst.HTTPPort = st.HTTPPort
	if inst.StartedAt.IsZero() {
		inst.StartedAt = now
	}
	inst.LastSeenAt = now
	_ = manager.RecordInstance(store, inst)

	t := time.NewTicker(10 * time.Second)
	defer t.Stop()

	for {
		select {
		case err := <-st.Fatal():
			_ = manager.RemoveInstance(store, instanceID)
			return err
		case <-ctx.Done():
			_ = manager.RemoveInstance(store, instanceID)
			return nil
		case <-t.C:
			_ = manager.Heartbeat(store, instanceID, time.Now())
		}
	}
}

func newProxyListCmd(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List known proxy instances",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := config.NewStore(root.configPath)
			if err != nil {
				return err
			}
			cfg, err := store.Load()
			if err != nil {
				return err
			}

			hc := manager.HealthClient{Timeout: 500 * time.Millisecond}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "INSTANCE\tPROFILE\tPID\tHTTP\tSOCKS\tSTATUS\tLAST_SEEN")
			for _, inst := range cfg.Instances {
				status := "dead"
				if inst.DaemonPID > 0 && proc.IsAlive(inst.DaemonPID) {
					status = "alive"
					if inst.HTTPPort > 0 {
						if err := hc.CheckHTTPProxy(inst.HTTPPort, inst.ID); err != nil {
							status = "unhealthy"
						}
					}
				}
				profileName := inst.ProfileID
				for _, p := range cfg.Profiles {
					if p.ID == inst.ProfileID {
						profileName = p.Name
						break
					}
				}

				_, _ = fmt.Fprintf(
					w,
					"%s\t%s\t%d\t%d\t%d\t%s\t%s\n",
					inst.ID,
					profileName,
					inst.DaemonPID,
					inst.HTTPPort,
					inst.SocksPort,
					status,
					inst.LastSeenAt.Format(time.RFC3339),
				)
			}
			_ = w.Flush()
			return nil
		},
	}
	return cmd
}

func newProxyStopCmd(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop [instance-id]",
		Short: "Stop a proxy instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := config.NewStore(root.configPath)
			if err != nil {
				return err
			}
			cfg, err := store.Load()
			if err != nil {
				return err
			}

			id := args[0]
			var inst config.Instance
			found := false
			for _, it := range cfg.Instances {
				if it.ID == id {
					inst = it
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("instance %q not found", id)
			}

			if inst.DaemonPID > 0 && proc.IsAlive(inst.DaemonPID) {
				p, _ := os.FindProcess(inst.DaemonPID)
				_ = terminateProcess(p, 2*time.Second)
			}
			_ = manager.RemoveInstance(store, id)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Stopped instance %s\n", id)
			return nil
		},
	}
	return cmd
}

func newProxyPruneCmd(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove dead/unhealthy proxy instances from config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := config.NewStore(root.configPath)
			if err != nil {
				return err
			}

			hc := manager.HealthClient{Timeout: 500 * time.Millisecond}
			removed := 0

			if err := store.Update(func(cfg *config.Config) error {
				out := cfg.Instances[:0]
				for _, inst := range cfg.Instances {
					if inst.DaemonPID <= 0 || !proc.IsAlive(inst.DaemonPID) {
						removed++
						continue
					}
					if inst.HTTPPort > 0 {
						if err := hc.CheckHTTPProxy(inst.HTTPPort, inst.ID); err != nil {
							removed++
							continue
						}
					}
					out = append(out, inst)
				}
				cfg.Instances = out
				return nil
			}); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Pruned %d instances\n", removed)
			return nil
		},
	}
	return cmd
}

func newProxyDoctorCmd(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check required external tools and basic configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var issues []string

			if _, err := exec.LookPath("ssh"); err != nil {
				issues = append(issues, "missing `ssh` (OpenSSH client)")
			}
			if _, err := exec.LookPath("ssh-keygen"); err != nil {
				issues = append(issues, "missing `ssh-keygen` (optional, only needed for `init` key creation)")
			}

			store, err := config.NewStore(root.configPath)
			if err != nil {
				issues = append(issues, "config store error: "+err.Error())
			} else {
				// Ensure config dir is writable.
				dir := filepath.Dir(store.Path())
				if err := os.MkdirAll(dir, 0o700); err != nil {
					issues = append(issues, "cannot create config dir: "+err.Error())
				}
			}

			out := cmd.OutOrStdout()
			if len(issues) == 0 {
				_, _ = fmt.Fprintln(out, "OK: environment looks good.")
				return nil
			}

			_, _ = fmt.Fprintln(out, "Issues found:")
			for _, it := range issues {
				_, _ = fmt.Fprintf(out, " - %s\n", it)
			}

			_, _ = fmt.Fprintln(out, "\nInstall hints:")
			for _, line := range installHints() {
				_, _ = fmt.Fprintf(out, " - %s\n", line)
			}

			// Doctor is informational; do not fail CI on missing system tools.
			return nil
		},
	}
	return cmd
}

func installHints() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"macOS usually ships with ssh; if not: `xcode-select --install`",
		}
	case "windows":
		return []string{
			"Windows 10/11: enable/install OpenSSH Client in Optional Features",
			"or via winget: `winget install Microsoft.OpenSSH.Beta`",
		}
	case "linux":
		id := linuxOSReleaseID()
		switch id {
		case "ubuntu", "debian":
			return []string{"Debian/Ubuntu: `sudo apt-get update && sudo apt-get install -y openssh-client`"}
		case "centos", "rhel":
			return []string{"CentOS/RHEL: `sudo yum install -y openssh-clients`"}
		case "rocky", "almalinux", "fedora":
			return []string{"Rocky/Alma/Fedora: `sudo dnf install -y openssh-clients`"}
		default:
			return []string{"Linux: install OpenSSH client package (often `openssh-client` or `openssh-clients`)"} // generic
		}
	default:
		return []string{"Install OpenSSH client (`ssh`) using your OS package manager"}
	}
}

func linuxOSReleaseID() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return ""
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if strings.HasPrefix(line, "ID=") {
			v := strings.TrimPrefix(line, "ID=")
			v = strings.Trim(v, "\"")
			return strings.ToLower(v)
		}
	}
	return ""
}

// Keep these helpers local; they are also used by the stack package.
func pickFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return 0, err
	}
	return parsePort(portStr)
}

func parsePort(s string) (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+s)
	if err != nil {
		return 0, err
	}
	return addr.Port, nil
}
