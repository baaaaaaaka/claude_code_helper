package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

const claudePatchProxyRuntimeTestEnv = "CLAUDE_PATCH_PROXY_RUNTIME_TEST"
const claudePatchProxyProfileEnv = "CLAUDE_PATCH_PROXY_PROFILE"
const claudePatchProxyConfigPathEnv = "CLAUDE_PATCH_PROXY_CONFIG_PATH"

func requirePatchTestYoloArgs(t *testing.T, path string, configPath string, wantVersion string) []string {
	t.Helper()
	yoloArgs := resolveYoloBypassArgs(path, configPath)
	if len(yoloArgs) > 0 {
		return yoloArgs
	}
	if strings.TrimSpace(wantVersion) == defaultClaudePatchVersion {
		t.Fatalf("expected Claude %s to expose yolo bypass args", wantVersion)
	}
	t.Skip("Claude build does not expose yolo bypass flags")
	return nil
}

func TestClaudePatchBypassRuntimeIntegration(t *testing.T) {
	if os.Getenv("CLAUDE_PATCH_TEST") != "1" {
		t.Skip("set CLAUDE_PATCH_TEST=1 to run integration test")
	}
	if runtime.GOOS == "windows" {
		t.Skip("skip bypass runtime integration on windows")
	}

	wantVersion := strings.TrimSpace(os.Getenv("CLAUDE_PATCH_VERSION"))
	if wantVersion == "" {
		wantVersion = defaultClaudePatchVersion
	}
	installURL := strings.TrimSpace(os.Getenv("CLAUDE_PATCH_INSTALL_URL"))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	path, err := resolveClaudeForPatchTest(t, ctx, installURL, wantVersion)
	if err != nil {
		t.Fatalf("resolveClaudeForPatchTest: %v", err)
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "helper-config.json")
	yoloArgs := requirePatchTestYoloArgs(t, path, configPath, wantVersion)
	opts := exePatchOptions{
		enabledFlag:     true,
		policySettings:  true,
		glibcCompat:     exePatchGlibcCompatDefault(),
		glibcCompatRoot: exePatchGlibcCompatRootDefault(),
	}

	var patchLog bytes.Buffer
	outcome, err := maybePatchExecutable(append([]string{path}, yoloArgs...), opts, configPath, &patchLog)
	if err != nil {
		t.Fatalf("maybePatchExecutable: %v\n%s", err, patchLog.String())
	}
	if outcome == nil || !outcome.BuiltInClaudePatchActive {
		t.Fatalf("expected active built-in Claude patch, got %#v\n%s", outcome, patchLog.String())
	}
	if outcome.Applied && outcome.BackupPath != "" {
		t.Cleanup(func() {
			if restoreErr := restoreExecutableFromBackup(outcome); restoreErr != nil {
				t.Errorf("restoreExecutableFromBackup: %v", restoreErr)
			}
		})
	}

	configDir, projectDir := newRuntimeEnvDirs(t, tmpDir, runtimePromptSettings{})
	token := fmt.Sprintf("BYPASS_%d", time.Now().UnixNano())
	outFile := filepath.Join(projectDir, "bypass-proof.txt")
	command := fmt.Sprintf("printf %s > %s && printf BYPASS_OK", shQuote(token), shQuote(outFile))

	result := runClaudePromptCaseWithExtraArgs(
		t,
		ctx,
		path,
		outcome,
		configDir,
		projectDir,
		yoloArgs,
		runtimePromptCase{
			Name:              "patched-bypass-runtime",
			Command:           command,
			ExpectPrompt:      false,
			ExpectedToolRule:  command,
			ExpectedStdoutSub: "BYPASS_OK",
		},
	)

	assertRuntimePromptCase(t, result, runtimePromptCase{
		Name:              "patched-bypass-runtime",
		Command:           command,
		ExpectPrompt:      false,
		ExpectedToolRule:  command,
		ExpectedStdoutSub: "BYPASS_OK",
	})

	if result.Result != nil && len(result.Result.PermissionDenials) > 0 {
		t.Fatalf(
			"expected bypass mode to avoid permission denials, got %+v\ninit permissionMode=%q\nstderr:\n%s\nstdout:\n%s",
			result.Result.PermissionDenials,
			result.InitPermissionMode,
			result.Stderr,
			strings.Join(result.RawLines, "\n"),
		)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf(
			"expected bypass command to create %s: %v\ninit permissionMode=%q\nstderr:\n%s\nstdout:\n%s",
			outFile,
			err,
			result.InitPermissionMode,
			result.Stderr,
			strings.Join(result.RawLines, "\n"),
		)
	}
	if got := string(content); got != token {
		t.Fatalf(
			"expected bypass file content %q, got %q\ninit permissionMode=%q\nstderr:\n%s\nstdout:\n%s",
			token,
			got,
			result.InitPermissionMode,
			result.Stderr,
			strings.Join(result.RawLines, "\n"),
		)
	}
}

func TestClaudePatchBypassRuntimeIntegrationWithProxy(t *testing.T) {
	if os.Getenv(claudePatchProxyRuntimeTestEnv) != "1" {
		t.Skipf("set %s=1 to run proxy integration test", claudePatchProxyRuntimeTestEnv)
	}
	if runtime.GOOS == "windows" {
		t.Skip("skip bypass proxy integration on windows")
	}

	profile := strings.TrimSpace(os.Getenv(claudePatchProxyProfileEnv))
	if profile == "" {
		t.Skipf("set %s to a configured proxy profile", claudePatchProxyProfileEnv)
	}

	wantVersion := strings.TrimSpace(os.Getenv("CLAUDE_PATCH_VERSION"))
	if wantVersion == "" {
		wantVersion = defaultClaudePatchVersion
	}
	installURL := strings.TrimSpace(os.Getenv("CLAUDE_PATCH_INSTALL_URL"))

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	path, err := resolveClaudeForPatchTest(t, ctx, installURL, wantVersion)
	if err != nil {
		t.Fatalf("resolveClaudeForPatchTest: %v", err)
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "helper-config.json")
	yoloArgs := requirePatchTestYoloArgs(t, path, configPath, wantVersion)
	opts := exePatchOptions{
		enabledFlag:     true,
		policySettings:  true,
		glibcCompat:     exePatchGlibcCompatDefault(),
		glibcCompatRoot: exePatchGlibcCompatRootDefault(),
	}

	var patchLog bytes.Buffer
	outcome, err := maybePatchExecutable(append([]string{path}, yoloArgs...), opts, configPath, &patchLog)
	if err != nil {
		t.Fatalf("maybePatchExecutable: %v\n%s", err, patchLog.String())
	}
	if outcome == nil || !outcome.BuiltInClaudePatchActive {
		t.Fatalf("expected active built-in Claude patch, got %#v\n%s", outcome, patchLog.String())
	}
	if outcome.Applied && outcome.BackupPath != "" {
		t.Cleanup(func() {
			if restoreErr := restoreExecutableFromBackup(outcome); restoreErr != nil {
				t.Errorf("restoreExecutableFromBackup: %v", restoreErr)
			}
		})
	}

	configDir, projectDir := newRuntimeEnvDirs(t, tmpDir, runtimePromptSettings{})
	token := fmt.Sprintf("BYPASS_PROXY_%d", time.Now().UnixNano())
	outFile := filepath.Join(projectDir, "bypass-proxy-proof.txt")
	command := fmt.Sprintf("printf %s > %s && printf BYPASS_OK", shQuote(token), shQuote(outFile))

	repoRoot := repoRootForTest(t)
	proxyBin := filepath.Join(t.TempDir(), "claude-proxy-test")
	if runtime.GOOS == "windows" {
		proxyBin += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", proxyBin, "./cmd/claude-proxy")
	build.Dir = repoRoot
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build claude-proxy test binary: %v\n%s", err, string(output))
	}

	proxyConfigPath := ensureProxyRuntimeConfig(t, tmpDir, profile)

	launchArgs := append([]string{path}, yoloArgs...)
	launchArgs = append(launchArgs,
		"--print",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-prompt-tool", "stdio",
		"--tools", "Bash",
		"--setting-sources", "user,project,local",
		"--append-system-prompt", runtimeRuleSystemPrompt,
		"--max-budget-usd", "0.15",
	)
	claudeArgs := commandArgsForOutcome(outcome, launchArgs)
	proxyArgs := make([]string, 0, 4+len(claudeArgs))
	if strings.TrimSpace(proxyConfigPath) != "" {
		proxyArgs = append(proxyArgs, "--config", proxyConfigPath)
	}
	proxyArgs = append(proxyArgs, "run", profile, "--")
	proxyArgs = append(proxyArgs, claudeArgs...)
	result := runPromptCaseWithCommand(
		t,
		ctx,
		proxyBin,
		proxyArgs,
		runtimeCommandEnv(configDir),
		projectDir,
		runtimePromptCase{
			Name:              "patched-bypass-runtime-proxy",
			Command:           command,
			ExpectPrompt:      false,
			ExpectedToolRule:  command,
			ExpectedStdoutSub: "BYPASS_OK",
		},
	)

	assertRuntimePromptCase(t, result, runtimePromptCase{
		Name:              "patched-bypass-runtime-proxy",
		Command:           command,
		ExpectPrompt:      false,
		ExpectedToolRule:  command,
		ExpectedStdoutSub: "BYPASS_OK",
	})

	if result.Result != nil && len(result.Result.PermissionDenials) > 0 {
		t.Fatalf(
			"expected bypass+proxy mode to avoid permission denials, got %+v\ninit permissionMode=%q\nstderr:\n%s\nstdout:\n%s",
			result.Result.PermissionDenials,
			result.InitPermissionMode,
			result.Stderr,
			strings.Join(result.RawLines, "\n"),
		)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf(
			"expected bypass+proxy command to create %s: %v\ninit permissionMode=%q\nstderr:\n%s\nstdout:\n%s",
			outFile,
			err,
			result.InitPermissionMode,
			result.Stderr,
			strings.Join(result.RawLines, "\n"),
		)
	}
	if got := string(content); got != token {
		t.Fatalf(
			"expected bypass+proxy file content %q, got %q\ninit permissionMode=%q\nstderr:\n%s\nstdout:\n%s",
			token,
			got,
			result.InitPermissionMode,
			result.Stderr,
			strings.Join(result.RawLines, "\n"),
		)
	}
}

func ensureProxyRuntimeConfig(t *testing.T, dir string, profile string) string {
	t.Helper()
	if configPath := strings.TrimSpace(os.Getenv(claudePatchProxyConfigPathEnv)); configPath != "" {
		return configPath
	}

	host := strings.TrimSpace(os.Getenv("SSH_TEST_HOST"))
	portText := strings.TrimSpace(os.Getenv("SSH_TEST_PORT"))
	user := strings.TrimSpace(os.Getenv("SSH_TEST_USER"))
	keyPath := strings.TrimSpace(os.Getenv("SSH_TEST_KEY"))
	if host == "" || portText == "" || user == "" || keyPath == "" {
		return ""
	}

	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse SSH_TEST_PORT %q: %v", portText, err)
	}
	configPath := filepath.Join(dir, "proxy-runtime-config.json")
	store, err := config.NewStore(configPath)
	if err != nil {
		t.Fatalf("new proxy runtime config store: %v", err)
	}
	enabled := true
	cfg := config.Config{
		Version:      config.CurrentVersion,
		ProxyEnabled: &enabled,
		Profiles: []config.Profile{{
			ID:   profile,
			Name: profile,
			Host: host,
			Port: port,
			User: user,
			SSHArgs: []string{
				"-i", keyPath,
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "BatchMode=yes",
			},
		}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save proxy runtime config: %v", err)
	}
	return configPath
}

func repoRootForTest(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("locate repo root from %s: %v", wd, err)
	}
	return root
}

func runPromptCaseWithCommand(
	t *testing.T,
	ctx context.Context,
	command string,
	args []string,
	env []string,
	dir string,
	tc runtimePromptCase,
) runtimePromptResult {
	t.Helper()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start prompt process: %v", err)
	}

	var result runtimePromptResult
	var stdinMu sync.Mutex
	stdinClosed := false
	closeStdin := func() {
		stdinMu.Lock()
		defer stdinMu.Unlock()
		if stdinClosed {
			return
		}
		stdinClosed = true
		_ = stdin.Close()
	}
	writeLine := func(line string) error {
		stdinMu.Lock()
		defer stdinMu.Unlock()
		if stdinClosed {
			return fmt.Errorf("stdin already closed")
		}
		_, err := io.WriteString(stdin, line+"\n")
		return err
	}

	userPrompt := fmt.Sprintf(
		"Run the exact Bash command `%s` exactly once. Do not simulate it. After it returns, answer with the exact stdout only.",
		tc.Command,
	)
	promptPayload, err := json.Marshal(map[string]any{
		"type":               "user",
		"session_id":         "",
		"parent_tool_use_id": nil,
		"message": map[string]any{
			"role":    "user",
			"content": userPrompt,
		},
	})
	if err != nil {
		t.Fatalf("marshal prompt payload: %v", err)
	}
	if err := writeLine(string(promptPayload)); err != nil {
		t.Fatalf("write prompt line: %v", err)
	}
	if !tc.ExpectPrompt {
		closeStdin()
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		result.RawLines = append(result.RawLines, line)

		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}

		switch envelope.Type {
		case "system":
			var event struct {
				Subtype        string `json:"subtype"`
				PermissionMode string `json:"permissionMode"`
			}
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue
			}
			if event.Subtype == "init" {
				result.InitPermissionMode = strings.TrimSpace(event.PermissionMode)
			}
		case "control_request":
			var req struct {
				RequestID string `json:"request_id"`
				Request   struct {
					Subtype  string `json:"subtype"`
					ToolName string `json:"tool_name"`
					Input    struct {
						Command string `json:"command"`
					} `json:"input"`
				} `json:"request"`
			}
			if err := json.Unmarshal([]byte(line), &req); err != nil {
				continue
			}
			if req.Request.Subtype != "can_use_tool" {
				continue
			}
			result.PermissionRequests = append(result.PermissionRequests, runtimePermissionRequest{
				RequestID: req.RequestID,
				ToolName:  req.Request.ToolName,
				Command:   req.Request.Input.Command,
			})
			response := fmt.Sprintf(
				`{"type":"control_response","response":{"subtype":"success","request_id":"%s","response":{"behavior":"allow","updatedInput":{}}}}`,
				req.RequestID,
			)
			if err := writeLine(response); err != nil {
				t.Fatalf("write permission response: %v", err)
			}
			closeStdin()
		case "assistant":
			var msg struct {
				Message struct {
					Content []struct {
						Type  string `json:"type"`
						ID    string `json:"id"`
						Name  string `json:"name"`
						Input struct {
							Command string `json:"command"`
						} `json:"input"`
					} `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			for _, block := range msg.Message.Content {
				if block.Type != "tool_use" {
					continue
				}
				result.ToolUses = append(result.ToolUses, runtimeToolUse{
					ToolUseID: block.ID,
					Name:      block.Name,
					Command:   block.Input.Command,
				})
			}
		case "user":
			var msg struct {
				Message struct {
					Content []struct {
						Type      string          `json:"type"`
						Content   json.RawMessage `json:"content"`
						IsError   bool            `json:"is_error"`
						ToolUseID string          `json:"tool_use_id"`
					} `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			for _, block := range msg.Message.Content {
				if block.Type != "tool_result" {
					continue
				}
				result.ToolResults = append(result.ToolResults, runtimeToolResult{
					ToolUseID: block.ToolUseID,
					Content:   decodeToolResultContent(block.Content),
					IsError:   block.IsError,
				})
			}
		case "result":
			var event runtimeResultEvent
			if err := json.Unmarshal([]byte(line), &event); err == nil {
				result.Result = &event
			}
			closeStdin()
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan prompt stdout: %v", err)
	}

	waitErr := cmd.Wait()
	result.Stderr = stderr.String()
	if waitErr != nil {
		t.Fatalf("wait prompt process: %v\nstderr:\n%s\nstdout:\n%s", waitErr, result.Stderr, strings.Join(result.RawLines, "\n"))
	}
	return result
}
