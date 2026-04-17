package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/env"
	"github.com/ulikunitz/xz"
)

const (
	claudeNPMInstallDirName = "npm-install"
	claudeNPMInstallPackage = "@anthropic-ai/claude-code@latest"
	claudeNPMMinimumNode    = 18
	claudeNPMBootstrapNode  = "v18.20.8"
)

const (
	claudeNPMBootstrapDownloadTimeout = 2 * time.Minute
)

type managedNPMClaudeLayout struct {
	RootDir            string
	PrefixDir          string
	LauncherDir        string
	NodePath           string
	WrapperPath        string
	CLIPath            string
	RuntimeDir         string
	RuntimeNodePath    string
	RuntimeNPMPath     string
	RuntimeArchivePath string
}

type managedNPMNodeRuntime struct {
	NodePath      string
	NPMPath       string
	LaunchOutcome *patchOutcome
	Bootstrapped  bool
}

var (
	ensureManagedNPMBootstrapRuntimeFn = ensureManagedNPMBootstrapRuntime
	managedNPMNodeArchiveURLFn         = managedNPMNodeArchiveURL
	downloadManagedNPMNodeArchiveFn    = downloadURLToFileWithProxy
	extractManagedNPMNodeArchiveFn     = extractManagedNPMNodeArchive
)

func claudeRequiresNPMInstall(goos string) bool {
	if !strings.EqualFold(strings.TrimSpace(goos), "linux") || runtime.GOOS != "linux" {
		return false
	}
	_, unsupported := bunLinuxKernelCompatibilityProblem()
	return unsupported
}

func defaultManagedClaudeHostRoot(goos string, getenv func(string) string) string {
	if !strings.EqualFold(goos, "linux") {
		return ""
	}

	cacheBase := strings.TrimSpace(getenv("XDG_CACHE_HOME"))
	if cacheBase == "" {
		if home := strings.TrimSpace(getenv("HOME")); home != "" {
			cacheBase = filepath.Join(home, ".cache")
		}
	}
	if cacheBase == "" {
		var err error
		cacheBase, err = resolveStableCacheBase()
		if err != nil {
			return ""
		}
	}

	hostID := strings.TrimSpace(getenv(claudeProxyHostIDEnv))
	if hostID != "" {
		hostID = sanitizePathComponent(hostID)
	} else {
		hostID = resolveHostID()
	}
	if hostID == "" {
		return ""
	}

	return filepath.Join(cacheBase, "claude-proxy", "hosts", hostID)
}

func defaultManagedNPMClaudeLayout(goos string, getenv func(string) string) (managedNPMClaudeLayout, bool) {
	hostRoot := defaultManagedClaudeHostRoot(goos, getenv)
	if hostRoot == "" {
		return managedNPMClaudeLayout{}, false
	}
	root := filepath.Join(hostRoot, claudeNPMInstallDirName)
	prefix := filepath.Join(root, "prefix")
	launcherDir := filepath.Join(root, "bin")
	runtimeDir, runtimeNodePath, runtimeNPMPath, runtimeArchivePath := managedNPMBootstrapPaths(root)
	return managedNPMClaudeLayout{
		RootDir:     root,
		PrefixDir:   prefix,
		LauncherDir: launcherDir,
		NodePath:    filepath.Join(launcherDir, "node"),
		WrapperPath: filepath.Join(root, "claude"),
		CLIPath: filepath.Join(
			prefix,
			"lib",
			"node_modules",
			"@anthropic-ai",
			"claude-code",
			"cli.js",
		),
		RuntimeDir:         runtimeDir,
		RuntimeNodePath:    runtimeNodePath,
		RuntimeNPMPath:     runtimeNPMPath,
		RuntimeArchivePath: runtimeArchivePath,
	}, true
}

func defaultManagedNPMClaudeLauncherCandidate(goos string, getenv func(string) string) string {
	layout, ok := defaultManagedNPMClaudeLayout(goos, getenv)
	if !ok || !managedNPMClaudeLayoutUsable(layout) {
		return ""
	}
	return layout.WrapperPath
}

func isManagedNPMClaudeLauncherPath(path string, getenv func(string) string) bool {
	layout, ok := defaultManagedNPMClaudeLayout(claudeInstallGOOS, getenv)
	if !ok {
		return false
	}

	actual := strings.TrimSpace(path)
	expected := strings.TrimSpace(layout.WrapperPath)
	if actual == "" || expected == "" {
		return false
	}

	actual = filepath.Clean(actual)
	expected = filepath.Clean(expected)
	if config.PathsEqual(actual, expected) {
		return true
	}

	if resolved, err := filepath.EvalSymlinks(actual); err == nil && config.PathsEqual(resolved, expected) {
		return true
	}
	if resolvedExpected, err := filepath.EvalSymlinks(expected); err == nil && config.PathsEqual(actual, resolvedExpected) {
		return true
	}
	return false
}

func resolveManagedNPMClaudeLayout() (managedNPMClaudeLayout, error) {
	hostRoot, _, err := resolveClaudeProxyHostRoot()
	if err != nil {
		return managedNPMClaudeLayout{}, fmt.Errorf("resolve claude-proxy host root for npm Claude install: %w", err)
	}
	root := filepath.Join(hostRoot, claudeNPMInstallDirName)
	prefix := filepath.Join(root, "prefix")
	launcherDir := filepath.Join(root, "bin")
	runtimeDir, runtimeNodePath, runtimeNPMPath, runtimeArchivePath := managedNPMBootstrapPaths(root)
	return managedNPMClaudeLayout{
		RootDir:     root,
		PrefixDir:   prefix,
		LauncherDir: launcherDir,
		NodePath:    filepath.Join(launcherDir, "node"),
		WrapperPath: filepath.Join(root, "claude"),
		CLIPath: filepath.Join(
			prefix,
			"lib",
			"node_modules",
			"@anthropic-ai",
			"claude-code",
			"cli.js",
		),
		RuntimeDir:         runtimeDir,
		RuntimeNodePath:    runtimeNodePath,
		RuntimeNPMPath:     runtimeNPMPath,
		RuntimeArchivePath: runtimeArchivePath,
	}, nil
}

func managedNPMBootstrapPaths(root string) (runtimeDir string, nodePath string, npmPath string, archivePath string) {
	arch, ok := managedNPMBootstrapArch(runtime.GOARCH)
	if !ok {
		return "", "", "", ""
	}
	base := fmt.Sprintf("node-%s-linux-%s", claudeNPMBootstrapNode, arch)
	runtimeDir = filepath.Join(root, "runtime", base)
	nodePath = filepath.Join(runtimeDir, "bin", "node")
	npmPath = filepath.Join(runtimeDir, "bin", "npm")
	archivePath = filepath.Join(root, "downloads", base+".tar.xz")
	return runtimeDir, nodePath, npmPath, archivePath
}

func managedNPMBootstrapArch(goarch string) (string, bool) {
	switch strings.TrimSpace(goarch) {
	case "amd64":
		return "x64", true
	case "arm64":
		return "arm64", true
	default:
		return "", false
	}
}

func managedNPMClaudeLayoutUsable(layout managedNPMClaudeLayout) bool {
	if !executableExists(layout.WrapperPath) || !executableExists(layout.NodePath) || !fileExists(layout.CLIPath) {
		return false
	}

	wrapperArgs, ok := readManagedNPMExecWrapperArgs(layout.WrapperPath)
	if !ok || len(wrapperArgs) < 2 {
		return false
	}
	if !config.PathsEqual(wrapperArgs[0], layout.NodePath) || !config.PathsEqual(wrapperArgs[1], layout.CLIPath) {
		return false
	}
	if !managedNPMExecWrapperTargetsUsable(wrapperArgs) {
		return false
	}

	nodeArgs, ok := readManagedNPMExecWrapperArgs(layout.NodePath)
	if !ok || len(nodeArgs) == 0 {
		return false
	}
	return managedNPMExecWrapperTargetsUsable(nodeArgs)
}

func readManagedNPMExecWrapperArgs(path string) ([]string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "exec ") || !strings.HasSuffix(line, ` "$@"`) {
			continue
		}
		body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "exec "), ` "$@"`))
		args, ok := parseManagedNPMExecWrapperArgList(body)
		if ok && len(args) > 0 {
			return args, true
		}
		return nil, false
	}
	return nil, false
}

func parseManagedNPMExecWrapperArgList(body string) ([]string, bool) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, false
	}
	args := make([]string, 0, 4)
	for pos := 0; pos < len(body); {
		for pos < len(body) && body[pos] == ' ' {
			pos++
		}
		if pos >= len(body) {
			break
		}
		if body[pos] != '\'' {
			return nil, false
		}
		pos++
		var arg strings.Builder
		for {
			for pos < len(body) && body[pos] != '\'' {
				arg.WriteByte(body[pos])
				pos++
			}
			if pos >= len(body) {
				return nil, false
			}
			pos++
			if strings.HasPrefix(body[pos:], `\''`) {
				arg.WriteByte('\'')
				pos += len(`\''`)
				continue
			}
			break
		}
		args = append(args, arg.String())
		for pos < len(body) && body[pos] == ' ' {
			pos++
		}
	}
	return args, len(args) > 0
}

func managedNPMExecWrapperTargetsUsable(args []string) bool {
	for idx, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			return false
		}
		if !filepath.IsAbs(arg) {
			continue
		}
		if idx == 0 {
			if !executableExists(arg) {
				return false
			}
			continue
		}
		if !fileExists(arg) {
			return false
		}
	}
	return true
}

func runNPMClaudeInstallerWithEnv(ctx context.Context, out io.Writer, proxyURL string, extraEnv []string) error {
	layout, err := resolveManagedNPMClaudeLayout()
	if err != nil {
		return err
	}

	nodeRuntime, err := resolveManagedNPMNodeRuntime(ctx, layout, proxyURL, out)
	if err != nil {
		return claudeNPMFallbackError(err)
	}

	if out != nil {
		if reason, unsupported := bunLinuxKernelCompatibilityProblem(); unsupported {
			_, _ = fmt.Fprintln(out, reason)
		}
		_, _ = fmt.Fprintf(out, "Linux kernel is too old for Claude Code's bundled Bun runtime; installing the npm distribution under %s.\n", layout.RootDir)
	}

	installErr := installManagedNPMClaudeWithRuntime(ctx, out, layout, nodeRuntime, proxyURL, extraEnv)
	if installErr == nil {
		return nil
	}

	if !nodeRuntime.Bootstrapped {
		if out != nil {
			_, _ = fmt.Fprintf(out, "The detected npm toolchain could not install a working Claude Code CLI; retrying with a claude-proxy-managed Node.js runtime under %s.\n", layout.RootDir)
		}
		bootstrapRuntime, bootstrapErr := ensureManagedNPMBootstrapRuntimeFn(ctx, layout, proxyURL, out)
		if bootstrapErr != nil {
			return claudeNPMFallbackError(fmt.Errorf("%w; automatic private Node.js bootstrap failed after system npm fallback error: %w", installErr, bootstrapErr))
		}
		if retryErr := installManagedNPMClaudeWithRuntime(ctx, out, layout, bootstrapRuntime, proxyURL, extraEnv); retryErr == nil {
			return nil
		} else {
			return claudeNPMFallbackError(fmt.Errorf("%w; retry with a claude-proxy-managed Node.js runtime also failed: %w", installErr, retryErr))
		}
	}

	return claudeNPMFallbackError(installErr)
}

func claudeNPMFallbackError(err error) error {
	if err == nil {
		return nil
	}
	reason, unsupported := bunLinuxKernelCompatibilityProblem()
	if !unsupported {
		return err
	}
	return fmt.Errorf(
		"%s; npm fallback requires a usable Node.js >= %d runtime with npm: %w",
		reason,
		claudeNPMMinimumNode,
		err,
	)
}

func verifyManagedNPMNodeVersion(ctx context.Context, nodePath string) error {
	out, err := runManagedNPMNodeCommandCombinedOutput(ctx, managedNPMNodeRuntime{NodePath: nodePath}, "--version")
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			return fmt.Errorf("probe node version: %w", err)
		}
		return fmt.Errorf("probe node version: %w (%s)", err, text)
	}
	major, ok := parseNodeVersionMajor(string(out))
	if !ok {
		return fmt.Errorf("parse node version output %q", strings.TrimSpace(string(out)))
	}
	if major < claudeNPMMinimumNode {
		return fmt.Errorf("detected Node.js %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func resolveManagedNPMNodeRuntime(ctx context.Context, layout managedNPMClaudeLayout, proxyURL string, log io.Writer) (managedNPMNodeRuntime, error) {
	nodePath, nodeErr := claudeInstallLookPathFn("node")
	npmPath, npmErr := claudeInstallLookPathFn("npm")
	if nodeErr == nil {
		runtime, err := resolveManagedNPMNodeRuntimeFromPath(ctx, nodePath, log)
		if err == nil && npmErr == nil {
			runtime.NPMPath = npmPath
			return runtime, nil
		}
		if err == nil && npmErr != nil {
			if log != nil {
				_, _ = fmt.Fprintf(log, "npm was not found in PATH; installing a claude-proxy-managed Node.js runtime under %s.\n", layout.RootDir)
			}
		} else if err != nil {
			if log != nil {
				_, _ = fmt.Fprintf(log, "System Node.js runtime at %s is unsuitable for npm fallback (%v); installing a claude-proxy-managed Node.js runtime under %s.\n", nodePath, err, layout.RootDir)
			}
		}
		bootstrapRuntime, bootstrapErr := ensureManagedNPMBootstrapRuntimeFn(ctx, layout, proxyURL, log)
		if bootstrapErr == nil {
			return bootstrapRuntime, nil
		}
		if err != nil {
			return managedNPMNodeRuntime{}, fmt.Errorf("%w; automatic private Node.js bootstrap failed: %w", err, bootstrapErr)
		}
		return managedNPMNodeRuntime{}, fmt.Errorf("npm was not found in PATH; automatic private Node.js bootstrap failed: %w", bootstrapErr)
	}

	if log != nil {
		_, _ = fmt.Fprintf(log, "node was not found in PATH; installing a claude-proxy-managed Node.js runtime under %s.\n", layout.RootDir)
	}
	bootstrapRuntime, bootstrapErr := ensureManagedNPMBootstrapRuntimeFn(ctx, layout, proxyURL, log)
	if bootstrapErr == nil {
		return bootstrapRuntime, nil
	}
	if npmErr == nil {
		return managedNPMNodeRuntime{}, fmt.Errorf("node was not found in PATH; automatic private Node.js bootstrap failed: %w", bootstrapErr)
	}
	return managedNPMNodeRuntime{}, fmt.Errorf("node was not found in PATH; automatic private Node.js bootstrap failed: %w", bootstrapErr)
}

func resolveManagedNPMNodeRuntimeFromPath(ctx context.Context, nodePath string, log io.Writer) (managedNPMNodeRuntime, error) {
	nodePath = filepath.Clean(strings.TrimSpace(nodePath))
	if nodePath == "" {
		return managedNPMNodeRuntime{}, fmt.Errorf("node path is empty")
	}

	runtime := managedNPMNodeRuntime{NodePath: nodePath}
	if err := verifyManagedNPMNodeVersion(ctx, runtime.NodePath); err == nil {
		return runtime, nil
	} else if !shouldApplyManagedNPMNodeGlibcCompat(err) {
		return managedNPMNodeRuntime{}, annotateManagedNPMNodeRuntimeError(err)
	}

	if log != nil {
		_, _ = fmt.Fprintf(log, "Node.js at %s needs glibc compat on this host; preparing a claude-proxy-managed Node launcher.\n", nodePath)
	}

	prepared, err := prepareManagedNPMNodeGlibcCompat(nodePath, log)
	if err != nil {
		return managedNPMNodeRuntime{}, err
	}
	runtime.LaunchOutcome = prepared
	if err := verifyManagedNPMNodeVersionWithRuntime(ctx, runtime); err != nil {
		return managedNPMNodeRuntime{}, annotateManagedNPMNodeRuntimeError(err)
	}
	return runtime, nil
}

func ensureManagedNPMBootstrapRuntime(ctx context.Context, layout managedNPMClaudeLayout, proxyURL string, log io.Writer) (managedNPMNodeRuntime, error) {
	if strings.TrimSpace(layout.RuntimeNodePath) == "" || strings.TrimSpace(layout.RuntimeNPMPath) == "" || strings.TrimSpace(layout.RuntimeDir) == "" {
		return managedNPMNodeRuntime{}, fmt.Errorf("automatic private Node.js bootstrap unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	if executableExists(layout.RuntimeNodePath) && fileExists(layout.RuntimeNPMPath) {
		runtime, err := resolveManagedNPMNodeRuntimeFromPath(ctx, layout.RuntimeNodePath, log)
		if err == nil {
			runtime.NPMPath = layout.RuntimeNPMPath
			runtime.Bootstrapped = true
			return runtime, nil
		}
		if log != nil {
			_, _ = fmt.Fprintf(log, "Existing claude-proxy-managed Node.js runtime at %s is stale (%v); reinstalling it.\n", layout.RuntimeDir, err)
		}
	}

	if err := installManagedNPMBootstrapRuntime(ctx, layout, proxyURL, log); err != nil {
		return managedNPMNodeRuntime{}, err
	}
	runtime, err := resolveManagedNPMNodeRuntimeFromPath(ctx, layout.RuntimeNodePath, log)
	if err != nil {
		return managedNPMNodeRuntime{}, err
	}
	runtime.NPMPath = layout.RuntimeNPMPath
	runtime.Bootstrapped = true
	return runtime, nil
}

func installManagedNPMBootstrapRuntime(ctx context.Context, layout managedNPMClaudeLayout, proxyURL string, log io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	archiveURL, err := managedNPMNodeArchiveURLFn()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(layout.RuntimeArchivePath), 0o755); err != nil {
		return fmt.Errorf("create Node.js archive dir %s: %w", filepath.Dir(layout.RuntimeArchivePath), err)
	}
	if err := os.MkdirAll(filepath.Dir(layout.RuntimeDir), 0o755); err != nil {
		return fmt.Errorf("create Node.js runtime dir %s: %w", filepath.Dir(layout.RuntimeDir), err)
	}
	if log != nil {
		_, _ = fmt.Fprintf(log, "Installing a claude-proxy-managed Node.js runtime (%s) under %s.\n", claudeNPMBootstrapNode, layout.RuntimeDir)
	}

	if !fileExists(layout.RuntimeArchivePath) {
		if err := downloadManagedNPMNodeArchiveFn(archiveURL, layout.RuntimeArchivePath, claudeNPMBootstrapDownloadTimeout, proxyURL); err != nil {
			return fmt.Errorf("download private Node.js runtime: %w", err)
		}
	}

	stageDir, err := os.MkdirTemp(filepath.Dir(layout.RuntimeDir), "node-runtime-stage-")
	if err != nil {
		return fmt.Errorf("create Node.js runtime staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(stageDir) }()

	if err := extractManagedNPMNodeArchiveFn(layout.RuntimeArchivePath, stageDir); err != nil {
		return fmt.Errorf("extract private Node.js runtime: %w", err)
	}
	if !executableExists(filepath.Join(stageDir, "bin", "node")) || !fileExists(filepath.Join(stageDir, "bin", "npm")) {
		return fmt.Errorf("private Node.js runtime archive did not contain bin/node and bin/npm")
	}

	if err := os.RemoveAll(layout.RuntimeDir); err != nil {
		return fmt.Errorf("replace Node.js runtime at %s: %w", layout.RuntimeDir, err)
	}
	if err := os.Rename(stageDir, layout.RuntimeDir); err != nil {
		return fmt.Errorf("install Node.js runtime under %s: %w", layout.RuntimeDir, err)
	}
	return nil
}

func managedNPMNodeArchiveURL() (string, error) {
	arch, ok := managedNPMBootstrapArch(runtime.GOARCH)
	if !ok {
		return "", fmt.Errorf("automatic private Node.js bootstrap unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("https://nodejs.org/dist/%s/node-%s-linux-%s.tar.xz", claudeNPMBootstrapNode, claudeNPMBootstrapNode, arch), nil
}

func downloadURLToFileWithProxy(rawURL string, targetPath string, timeout time.Duration, proxyURL string) error {
	if strings.TrimSpace(proxyURL) == "" {
		return downloadURLToFile(rawURL, targetPath, timeout)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}
	tmpPath := targetPath + ".tmp"
	_ = os.Remove(tmpPath)

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "claude-proxy")
	req.Header.Set("Accept", "application/octet-stream")

	proxyParsed, err := neturl.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("parse proxy url %q: %w", proxyURL, err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyParsed)
	client := &http.Client{Timeout: timeout, Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download %s failed: %s", rawURL, resp.Status)
	}

	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func extractManagedNPMNodeArchive(archivePath string, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	xzr, err := xz.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(xzr)
	dest = filepath.Clean(dest)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		relPath, ok := stripManagedNPMArchiveRoot(hdr.Name)
		if !ok {
			continue
		}
		target := filepath.Join(dest, relPath)
		if !pathWithinRoot(dest, target) {
			return fmt.Errorf("archive entry escapes destination: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeManagedNPMArchiveFile(target, tr, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.RemoveAll(target); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			linkTarget, ok := stripManagedNPMArchiveRoot(hdr.Linkname)
			if !ok {
				continue
			}
			linkTargetPath := filepath.Join(dest, linkTarget)
			if !pathWithinRoot(dest, linkTargetPath) {
				return fmt.Errorf("archive hard link escapes destination: %s", hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.RemoveAll(target); err != nil {
				return err
			}
			if err := os.Link(linkTargetPath, target); err != nil {
				return err
			}
		}
	}
}

func stripManagedNPMArchiveRoot(name string) (string, bool) {
	name = strings.TrimSpace(strings.TrimPrefix(name, "./"))
	name = strings.TrimLeft(name, "/")
	if name == "" {
		return "", false
	}
	parts := strings.Split(name, "/")
	if len(parts) < 2 {
		return "", false
	}
	rel := filepath.Clean(filepath.Join(parts[1:]...))
	if rel == "." || rel == "" {
		return "", false
	}
	return rel, true
}

func pathWithinRoot(root string, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if config.PathsEqual(root, target) {
		return true
	}
	return strings.HasPrefix(target, root+string(os.PathSeparator))
}

func writeManagedNPMArchiveFile(path string, r io.Reader, perm os.FileMode) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func verifyManagedNPMNodeVersionWithRuntime(ctx context.Context, runtime managedNPMNodeRuntime) error {
	out, err := runManagedNPMNodeCommandCombinedOutput(ctx, runtime, "--version")
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			return fmt.Errorf("probe node version: %w", err)
		}
		return fmt.Errorf("probe node version: %w (%s)", err, text)
	}
	major, ok := parseNodeVersionMajor(string(out))
	if !ok {
		return fmt.Errorf("parse node version output %q", strings.TrimSpace(string(out)))
	}
	if major < claudeNPMMinimumNode {
		return fmt.Errorf("detected Node.js %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func shouldApplyManagedNPMNodeGlibcCompat(err error) bool {
	if err == nil {
		return false
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return false
	}
	if !glibcCompatHostEligibleFn() || !exePatchEnabledDefault() || !exePatchGlibcCompatDefault() {
		return false
	}
	return isMissingManagedNPMCompatDependencyError(err.Error())
}

func annotateManagedNPMNodeRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	if !isMissingManagedNPMCPPCompatError(err.Error()) {
		return err
	}
	return fmt.Errorf("%w; the selected Node.js runtime also needs a newer libstdc++/libgcc runtime than this host provides", err)
}

func isMissingManagedNPMCompatDependencyError(text string) bool {
	return isMissingGlibcSymbolError(text) || isMissingManagedNPMCPPCompatError(text)
}

func isMissingManagedNPMCPPCompatError(text string) bool {
	if strings.Contains(text, "GLIBCXX_") || strings.Contains(text, "CXXABI_") {
		return true
	}
	lower := strings.ToLower(text)
	return strings.Contains(lower, "libstdc++.so.6") || strings.Contains(lower, "libgcc_s.so.1")
}

func prepareManagedNPMNodeGlibcCompat(nodePath string, log io.Writer) (*patchOutcome, error) {
	outcome, _, err := applyClaudeGlibcCompatPatchFn(nodePath, exePatchOptions{
		enabledFlag:     true,
		glibcCompat:     true,
		glibcCompatRoot: exePatchGlibcCompatRootDefault(),
	}, log, false, &patchOutcome{
		SourcePath:       nodePath,
		TargetPath:       nodePath,
		LaunchArgsPrefix: []string{nodePath},
	})
	if err != nil {
		return nil, err
	}
	if outcome == nil {
		return nil, fmt.Errorf("prepare glibc compat launch path for node: empty outcome")
	}
	return outcome, nil
}

func runManagedNPMNodeCommandCombinedOutput(ctx context.Context, runtime managedNPMNodeRuntime, args ...string) ([]byte, error) {
	cmdArgs := managedNPMNodeCommandArgs(runtime, args...)
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("missing managed npm node command")
	}
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	return cmd.CombinedOutput()
}

func managedNPMNodeCommandArgs(runtime managedNPMNodeRuntime, args ...string) []string {
	base := []string{runtime.NodePath}
	base = append(base, args...)
	return commandArgsForOutcome(runtime.LaunchOutcome, base)
}

func parseNodeVersionMajor(output string) (int, bool) {
	output = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(output), "v"))
	if output == "" {
		return 0, false
	}
	majorText, _, _ := strings.Cut(output, ".")
	major, err := strconv.Atoi(leadingDigits(majorText))
	if err != nil {
		return 0, false
	}
	return major, true
}

func writeManagedNPMNodeLauncher(layout managedNPMClaudeLayout, runtime managedNPMNodeRuntime) error {
	if err := os.MkdirAll(layout.LauncherDir, 0o755); err != nil {
		return fmt.Errorf("create npm Node launcher dir %s: %w", layout.LauncherDir, err)
	}
	return writeManagedNPMExecWrapper(layout.NodePath, managedNPMNodeCommandArgs(runtime))
}

func writeManagedNPMClaudeWrapper(layout managedNPMClaudeLayout) error {
	if err := os.MkdirAll(layout.RootDir, 0o755); err != nil {
		return fmt.Errorf("create npm Claude launcher dir %s: %w", layout.RootDir, err)
	}
	return writeManagedNPMExecWrapper(layout.WrapperPath, []string{layout.NodePath, layout.CLIPath})
}

func writeManagedNPMExecWrapper(path string, argv []string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("wrapper path is empty")
	}
	if len(argv) == 0 {
		return fmt.Errorf("wrapper command is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create wrapper dir %s: %w", filepath.Dir(path), err)
	}
	wrapperBody := "#!/bin/sh\nexec " + joinShellQuotedArgs(argv) + " \"$@\"\n"
	if err := os.WriteFile(path, []byte(wrapperBody), 0o755); err != nil {
		return fmt.Errorf("write wrapper %s: %w", path, err)
	}
	return nil
}

func joinShellQuotedArgs(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, shellQuotePOSIX(arg))
	}
	return strings.Join(parts, " ")
}

func prependInstallEnvPath(env []string, dir string) []string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return env
	}
	pathValue, _ := lookupInstallEnvValue(env, "PATH")
	if strings.TrimSpace(pathValue) == "" {
		return setInstallEnvValue(env, "PATH", dir)
	}
	return setInstallEnvValue(env, "PATH", dir+string(os.PathListSeparator)+pathValue)
}

func lookupInstallEnvValue(env []string, key string) (string, bool) {
	for _, entry := range env {
		k, value, ok := strings.Cut(entry, "=")
		if ok && sameInstallEnvKey(k, key) {
			return value, true
		}
	}
	return "", false
}

func installManagedNPMClaudeWithRuntime(
	ctx context.Context,
	out io.Writer,
	layout managedNPMClaudeLayout,
	runtime managedNPMNodeRuntime,
	proxyURL string,
	extraEnv []string,
) error {
	if err := writeManagedNPMNodeLauncher(layout, runtime); err != nil {
		return err
	}

	envList := managedNPMInstallEnv(layout, proxyURL, extraEnv)
	if err := verifyManagedNPMRuntimeTooling(ctx, runtime.NPMPath, envList); err != nil {
		return err
	}

	cmd := exec.CommandContext(
		ctx,
		runtime.NPMPath,
		"install",
		"-g",
		"--no-fund",
		"--no-audit",
		"--loglevel=error",
		claudeNPMInstallPackage,
	)
	cmd.Env = envList
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install Claude Code via npm: %w", err)
	}

	if !executableExists(layout.CLIPath) {
		return fmt.Errorf("npm install completed but %s was not found", layout.CLIPath)
	}
	if err := writeManagedNPMClaudeWrapper(layout); err != nil {
		return err
	}
	if err := verifyManagedNPMClaudeWrapper(ctx, layout.WrapperPath); err != nil {
		return err
	}
	if out != nil {
		_, _ = fmt.Fprintf(out, "Claude Code npm launcher ready at %s\n", layout.WrapperPath)
	}
	return nil
}

func managedNPMInstallEnv(layout managedNPMClaudeLayout, proxyURL string, extraEnv []string) []string {
	envList := sanitizeManagedNPMInstallEnv(os.Environ())
	if proxyURL != "" {
		envList = env.WithProxy(envList, proxyURL)
	}
	envList = setInstallEnvValue(envList, "npm_config_prefix", layout.PrefixDir)
	envList = setInstallEnvValue(envList, "NPM_CONFIG_PREFIX", layout.PrefixDir)
	envList = setInstallEnvValue(envList, "npm_config_update_notifier", "false")
	envList = setInstallEnvValue(envList, "NPM_CONFIG_UPDATE_NOTIFIER", "false")
	for _, kv := range extraEnv {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		envList = setInstallEnvValue(envList, key, value)
	}
	return prependInstallEnvPath(envList, layout.LauncherDir)
}

func sanitizeManagedNPMInstallEnv(envList []string) []string {
	out := make([]string, 0, len(envList))
	for _, entry := range envList {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if shouldDropManagedNPMInstallEnvKey(key) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func shouldDropManagedNPMInstallEnvKey(key string) bool {
	return sameInstallEnvKey(key, "npm_config_prefix") || sameInstallEnvKey(key, "NPM_CONFIG_PREFIX")
}

func verifyManagedNPMRuntimeTooling(ctx context.Context, npmPath string, envList []string) error {
	out, err := runManagedNPMCommandCombinedOutput(ctx, envList, npmPath, "--version")
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return fmt.Errorf("probe npm version with the selected Node.js runtime: %w", err)
	}
	return fmt.Errorf("probe npm version with the selected Node.js runtime: %w (%s)", err, text)
}

func verifyManagedNPMClaudeWrapper(ctx context.Context, wrapperPath string) error {
	out, err := runClaudeProbeWithContext(ctx, wrapperPath, "--version", 5*time.Second)
	version := extractVersion(out)
	if version != "" && strings.ContainsAny(version, "0123456789") {
		return nil
	}
	if err != nil {
		text := strings.TrimSpace(out)
		if text == "" {
			return fmt.Errorf("probe installed Claude Code npm launcher: %w", err)
		}
		return fmt.Errorf("probe installed Claude Code npm launcher: %w (%s)", err, text)
	}
	text := strings.TrimSpace(out)
	if text == "" {
		return fmt.Errorf("probe installed Claude Code npm launcher: empty version output")
	}
	return fmt.Errorf("probe installed Claude Code npm launcher: unexpected version output %q", text)
}

func runManagedNPMCommandCombinedOutput(ctx context.Context, envList []string, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = envList
	return cmd.CombinedOutput()
}

func shellQuotePOSIX(v string) string {
	if v == "" {
		return "''"
	}
	var buf bytes.Buffer
	buf.WriteByte('\'')
	for _, ch := range v {
		if ch == '\'' {
			buf.WriteString(`'\''`)
			continue
		}
		buf.WriteRune(ch)
	}
	buf.WriteByte('\'')
	return buf.String()
}
