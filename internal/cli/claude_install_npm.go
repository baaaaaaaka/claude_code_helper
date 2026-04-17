package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/env"
)

const (
	claudeNPMInstallDirName = "npm-install"
	claudeNPMInstallPackage = "@anthropic-ai/claude-code@latest"
	claudeNPMMinimumNode    = 18
)

type managedNPMClaudeLayout struct {
	RootDir     string
	PrefixDir   string
	LauncherDir string
	NodePath    string
	WrapperPath string
	CLIPath     string
}

type managedNPMNodeRuntime struct {
	NodePath      string
	LaunchOutcome *patchOutcome
}

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
	}, nil
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

	nodePath, err := claudeInstallLookPathFn("node")
	if err != nil {
		return claudeNPMFallbackError(fmt.Errorf("node was not found in PATH"))
	}
	nodeRuntime, err := resolveManagedNPMNodeRuntime(ctx, nodePath, out)
	if err != nil {
		return claudeNPMFallbackError(err)
	}

	npmPath, err := claudeInstallLookPathFn("npm")
	if err != nil {
		return claudeNPMFallbackError(fmt.Errorf("npm was not found in PATH"))
	}
	if err := writeManagedNPMNodeLauncher(layout, nodeRuntime); err != nil {
		return claudeNPMFallbackError(err)
	}

	if out != nil {
		if reason, unsupported := bunLinuxKernelCompatibilityProblem(); unsupported {
			_, _ = fmt.Fprintln(out, reason)
		}
		_, _ = fmt.Fprintf(out, "Linux kernel is too old for Claude Code's bundled Bun runtime; installing the npm distribution under %s.\n", layout.RootDir)
	}

	envList := append([]string{}, os.Environ()...)
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
	envList = prependInstallEnvPath(envList, layout.LauncherDir)

	cmd := exec.CommandContext(
		ctx,
		npmPath,
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
		return claudeNPMFallbackError(fmt.Errorf("install Claude Code via npm: %w", err))
	}

	if !executableExists(layout.CLIPath) {
		return claudeNPMFallbackError(fmt.Errorf("npm install completed but %s was not found", layout.CLIPath))
	}
	if err := writeManagedNPMClaudeWrapper(layout); err != nil {
		return claudeNPMFallbackError(err)
	}
	if out != nil {
		_, _ = fmt.Fprintf(out, "Claude Code npm launcher ready at %s\n", layout.WrapperPath)
	}
	return nil
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
		"%s; npm fallback requires Node.js >= %d and npm in PATH: %w",
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

func resolveManagedNPMNodeRuntime(ctx context.Context, nodePath string, log io.Writer) (managedNPMNodeRuntime, error) {
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
