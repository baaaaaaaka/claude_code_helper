package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const patchelfBinaryName = "patchelf"

type glibcCompatLayout struct {
	RootDir    string
	LibDir     string
	LoaderPath string
}

func applyClaudeGlibcCompatPatch(path string, opts exePatchOptions, log io.Writer, dryRun bool, outcome *patchOutcome) (*patchOutcome, bool, error) {
	if runtime.GOOS != "linux" || !opts.glibcCompatConfigured() {
		return outcome, false, nil
	}
	if log == nil {
		log = io.Discard
	}

	layout, err := resolveGlibcCompatLayout(opts.glibcCompatRoot)
	if err != nil {
		return outcome, false, err
	}
	if _, err := exec.LookPath(patchelfBinaryName); err != nil {
		return outcome, false, fmt.Errorf("missing %s in PATH: %w", patchelfBinaryName, err)
	}

	currentInterpreter, err := readPatchelfValue(path, "--print-interpreter")
	if err != nil {
		return outcome, false, fmt.Errorf("read interpreter: %w", err)
	}
	currentRPath, err := readPatchelfValue(path, "--print-rpath")
	if err != nil {
		return outcome, false, fmt.Errorf("read rpath: %w", err)
	}

	targetRPath := mergeRPath(layout.LibDir, currentRPath)
	if sameFilePath(currentInterpreter, layout.LoaderPath) && pathListContains(currentRPath, layout.LibDir) {
		_, _ = fmt.Fprintf(log, "exe-patch: glibc compat already configured for %s\n", path)
		return outcome, false, nil
	}
	if dryRun {
		_, _ = fmt.Fprintf(log, "exe-patch: dry-run enabled; would apply glibc compat patch to %s\n", path)
		return outcome, false, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return outcome, false, fmt.Errorf("stat executable for glibc patch: %w", err)
	}
	if outcome == nil {
		outcome = &patchOutcome{TargetPath: path}
	}
	if strings.TrimSpace(outcome.BackupPath) == "" {
		backupPath, err := backupExecutable(path, info.Mode().Perm())
		if err != nil {
			return outcome, false, fmt.Errorf("create backup for glibc patch: %w", err)
		}
		outcome.BackupPath = backupPath
	}

	if err := patchElfInterpreterAndRPath(path, layout.LoaderPath, targetRPath); err != nil {
		return outcome, false, err
	}

	outcome.TargetPath = path
	outcome.Applied = true
	_, _ = fmt.Fprintf(log, "exe-patch: applied glibc compat patch to %s (loader=%s)\n", path, layout.LoaderPath)
	return outcome, true, nil
}

func resolveGlibcCompatLayout(root string) (glibcCompatLayout, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return glibcCompatLayout{}, fmt.Errorf("glibc compat root is empty")
	}
	candidates := []string{
		filepath.Clean(root),
		filepath.Join(root, "glibc-2.31"),
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "glibc-") {
				continue
			}
			candidates = append(candidates, filepath.Join(root, entry.Name()))
		}
	}

	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		libDir := filepath.Join(candidate, "lib")
		loaderPath := filepath.Join(libDir, "ld-linux-x86-64.so.2")
		libcPath := filepath.Join(libDir, "libc.so.6")
		if fileExists(loaderPath) && fileExists(libcPath) {
			return glibcCompatLayout{
				RootDir:    candidate,
				LibDir:     libDir,
				LoaderPath: loaderPath,
			}, nil
		}
	}
	return glibcCompatLayout{}, fmt.Errorf("glibc compat runtime not found under %s", root)
}

func isMissingGlibcSymbolError(output string) bool {
	if !strings.Contains(output, "GLIBC_") {
		return false
	}
	return strings.Contains(strings.ToLower(output), "not found")
}

func readPatchelfValue(path string, flag string) (string, error) {
	out, err := runPatchelf(flag, path)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, msg)
	}
	return strings.TrimSpace(string(out)), nil
}

func patchElfInterpreterAndRPath(path string, loaderPath string, rpath string) error {
	out, err := runPatchelf("--set-interpreter", loaderPath, "--set-rpath", rpath, path)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

func runPatchelf(args ...string) ([]byte, error) {
	cmd := exec.Command(patchelfBinaryName, args...)
	return cmd.CombinedOutput()
}

func mergeRPath(preferred string, existing string) string {
	var merged []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		for _, item := range merged {
			if sameFilePath(item, path) {
				return
			}
		}
		merged = append(merged, path)
	}
	add(preferred)
	for _, part := range strings.Split(existing, ":") {
		add(part)
	}
	return strings.Join(merged, ":")
}

func pathListContains(pathList string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, path := range strings.Split(pathList, ":") {
		if sameFilePath(path, target) {
			return true
		}
	}
	return false
}

func sameFilePath(a string, b string) bool {
	a = filepath.Clean(strings.TrimSpace(a))
	b = filepath.Clean(strings.TrimSpace(b))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
