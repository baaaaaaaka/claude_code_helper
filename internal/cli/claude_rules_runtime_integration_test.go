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
	"strings"
	"sync"
	"testing"
	"time"
)

const claudeRulesRuntimeTestEnv = "CLAUDE_RULES_RUNTIME_TEST"
const claudeRulesRuntimeManagedPolicyEnv = "CLAUDE_RULES_RUNTIME_EXPECT_MANAGED_POLICY"
const claudeRulesRuntimeVersionEnv = "CLAUDE_RULES_RUNTIME_VERSION"
const claudeRulesRuntimeInstallURLEnv = "CLAUDE_RULES_RUNTIME_INSTALL_URL"

const runtimeRuleSystemPrompt = "For the next user request, if the user specifies an exact shell command in backticks, you must invoke Bash exactly once with that exact command and no other command. Do not simulate command output."

type runtimePermissionsSpec struct {
	Allow []string `json:"allow,omitempty"`
	Ask   []string `json:"ask,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

type runtimeSettingsSpec struct {
	Permissions *runtimePermissionsSpec `json:"permissions,omitempty"`
}

type runtimeSettingsSource struct {
	Source   string              `json:"source"`
	Settings runtimeSettingsSpec `json:"settings"`
}

type runtimeSettingsSnapshot struct {
	Effective runtimeSettingsSpec     `json:"effective"`
	Sources   []runtimeSettingsSource `json:"sources"`
}

type runtimePromptSettings struct {
	User    runtimeSettingsSpec
	Project runtimeSettingsSpec
	Local   runtimeSettingsSpec
}

type runtimePermissionRequest struct {
	RequestID string
	ToolName  string
	Command   string
}

type runtimeToolUse struct {
	ToolUseID string
	Name      string
	Command   string
}

type runtimeToolResult struct {
	ToolUseID string
	Content   string
	IsError   bool
}

type runtimePermissionDenial struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type runtimeResultEvent struct {
	Subtype           string                    `json:"subtype"`
	IsError           bool                      `json:"is_error"`
	Result            string                    `json:"result"`
	PermissionDenials []runtimePermissionDenial `json:"permission_denials"`
}

type runtimePromptResult struct {
	InitPermissionMode string
	PermissionRequests []runtimePermissionRequest
	ToolUses           []runtimeToolUse
	ToolResults        []runtimeToolResult
	Result             *runtimeResultEvent
	Stderr             string
	RawLines           []string
}

type runtimePromptCase struct {
	Name              string
	Settings          runtimePromptSettings
	Command           string
	ExpectPrompt      bool
	ExpectDenied      bool
	ExpectedToolRule  string
	ExpectedStdoutSub string
}

func TestClaudeRulesRuntimeIntegration(t *testing.T) {
	if os.Getenv(claudeRulesRuntimeTestEnv) != "1" {
		t.Skipf("set %s=1 to run live Claude rules integration tests", claudeRulesRuntimeTestEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	tmpDir := t.TempDir()
	binaryPath := resolveClaudeBinaryForRuntimeTest(t, ctx, tmpDir)

	if os.Getenv(claudeRulesRuntimeManagedPolicyEnv) == "1" {
		t.Run("UnpatchedManagedPolicyOverridesUserAllow", func(t *testing.T) {
			configDir, projectDir := newRuntimeEnvDirs(t, tmpDir, runtimePromptSettings{
				User: runtimeSettingsSpec{
					Permissions: &runtimePermissionsSpec{
						Allow: []string{"Bash(printf USER_ALLOW_EXACT)"},
					},
				},
			})

			result := runClaudePromptCase(
				t,
				ctx,
				binaryPath,
				nil,
				configDir,
				projectDir,
				runtimePromptCase{
					Name:              "unpatched-user-allow-exact",
					Command:           "printf USER_ALLOW_EXACT",
					ExpectPrompt:      true,
					ExpectedToolRule:  "printf USER_ALLOW_EXACT",
					ExpectedStdoutSub: "USER_ALLOW_EXACT",
				},
			)
			assertRuntimePromptCase(t, result, runtimePromptCase{
				Name:              "unpatched-user-allow-exact",
				Command:           "printf USER_ALLOW_EXACT",
				ExpectPrompt:      true,
				ExpectedToolRule:  "printf USER_ALLOW_EXACT",
				ExpectedStdoutSub: "USER_ALLOW_EXACT",
			})
		})
	} else {
		t.Logf("skip managed-policy runtime case; set %s=1 to require remote managed policy behavior", claudeRulesRuntimeManagedPolicyEnv)
	}

	var patchLog bytes.Buffer
	patchOutcome := patchClaudeCopyForRulesMode(t, binaryPath, filepath.Join(tmpDir, "helper-config.json"), &patchLog)

	cases := []runtimePromptCase{
		{
			Name: "patched-user-exact-allow",
			Settings: runtimePromptSettings{
				User: runtimeSettingsSpec{
					Permissions: &runtimePermissionsSpec{
						Allow: []string{"Bash(printf USER_ALLOW_EXACT)"},
					},
				},
			},
			Command:           "printf USER_ALLOW_EXACT",
			ExpectPrompt:      false,
			ExpectedToolRule:  "printf USER_ALLOW_EXACT",
			ExpectedStdoutSub: "USER_ALLOW_EXACT",
		},
		{
			Name: "patched-project-prefix-allow",
			Settings: runtimePromptSettings{
				Project: runtimeSettingsSpec{
					Permissions: &runtimePermissionsSpec{
						Allow: []string{"Bash(echo PROJECT_PREFIX:*)"},
					},
				},
			},
			Command:           "echo PROJECT_PREFIX alpha",
			ExpectPrompt:      false,
			ExpectedToolRule:  "echo PROJECT_PREFIX alpha",
			ExpectedStdoutSub: "PROJECT_PREFIX alpha",
		},
		{
			Name: "patched-local-wildcard-allow",
			Settings: runtimePromptSettings{
				Local: runtimeSettingsSpec{
					Permissions: &runtimePermissionsSpec{
						Allow: []string{"Bash(printf LOCAL-WILD*)"},
					},
				},
			},
			Command:           "printf LOCAL-WILD-42",
			ExpectPrompt:      false,
			ExpectedToolRule:  "printf LOCAL-WILD-42",
			ExpectedStdoutSub: "LOCAL-WILD-42",
		},
		{
			Name: "patched-local-ask-overrides-user-allow",
			Settings: runtimePromptSettings{
				User: runtimeSettingsSpec{
					Permissions: &runtimePermissionsSpec{
						Allow: []string{"Bash(printf LOCAL_ASK_WIN)"},
					},
				},
				Local: runtimeSettingsSpec{
					Permissions: &runtimePermissionsSpec{
						Ask: []string{"Bash(printf LOCAL_ASK_WIN)"},
					},
				},
			},
			Command:           "printf LOCAL_ASK_WIN",
			ExpectPrompt:      true,
			ExpectedToolRule:  "printf LOCAL_ASK_WIN",
			ExpectedStdoutSub: "LOCAL_ASK_WIN",
		},
		{
			Name: "patched-project-deny-overrides-user-allow",
			Settings: runtimePromptSettings{
				User: runtimeSettingsSpec{
					Permissions: &runtimePermissionsSpec{
						Allow: []string{"Bash(printf DENY_TAKES_PRECEDENCE)"},
					},
				},
				Project: runtimeSettingsSpec{
					Permissions: &runtimePermissionsSpec{
						Deny: []string{"Bash(printf DENY_TAKES_PRECEDENCE)"},
					},
				},
			},
			Command:          "printf DENY_TAKES_PRECEDENCE",
			ExpectPrompt:     false,
			ExpectDenied:     true,
			ExpectedToolRule: "printf DENY_TAKES_PRECEDENCE",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			configDir, projectDir := newRuntimeEnvDirs(t, tmpDir, tc.Settings)
			result := runClaudePromptCase(t, ctx, binaryPath, patchOutcome, configDir, projectDir, tc)
			assertRuntimePromptCase(t, result, tc)
		})
	}
}

func resolveClaudeBinaryForRuntimeTest(t *testing.T, ctx context.Context, tmpDir string) string {
	t.Helper()

	path, err := exec.LookPath("claude")
	if err != nil {
		version := strings.TrimSpace(os.Getenv(claudeRulesRuntimeVersionEnv))
		if version == "" {
			version = strings.TrimSpace(os.Getenv("CLAUDE_PATCH_VERSION"))
		}
		if version == "" {
			version = defaultClaudePatchVersion
		}
		installURL := strings.TrimSpace(os.Getenv(claudeRulesRuntimeInstallURLEnv))
		if installURL == "" {
			installURL = strings.TrimSpace(os.Getenv("CLAUDE_PATCH_INSTALL_URL"))
		}
		path, err = resolveClaudeForPatchTest(t, ctx, installURL, version)
		if err != nil {
			t.Fatalf("resolve runtime claude binary: %v", err)
		}
		return path
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && strings.TrimSpace(resolved) != "" {
		path = resolved
	}
	dst := filepath.Join(tmpDir, claudeBinaryName())
	if err := copyFile(path, dst); err != nil {
		t.Fatalf("copy installed claude: %v", err)
	}
	if err := ensureExecutable(dst); err != nil {
		t.Fatalf("ensure executable: %v", err)
	}
	return dst
}

func patchClaudeCopyForRulesMode(t *testing.T, binaryPath string, helperConfigPath string, log io.Writer) *patchOutcome {
	t.Helper()

	outcome, err := maybePatchExecutable([]string{binaryPath}, exePatchOptions{
		enabledFlag:               true,
		policySettings:            true,
		glibcCompat:               false,
		allowBuiltInWithoutBypass: true,
	}, helperConfigPath, log)
	if err != nil {
		t.Fatalf("patch copied claude for rules mode: %v", err)
	}
	if outcome == nil || !outcome.BuiltInClaudePatchActive {
		t.Fatalf("expected active built-in Claude patch, got %#v", outcome)
	}
	return outcome
}

func newRuntimeEnvDirs(t *testing.T, parent string, settings runtimePromptSettings) (string, string) {
	t.Helper()

	root := filepath.Join(parent, t.Name())
	configDir := filepath.Join(root, "config")
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	prepareRuntimeAuth(t, configDir)

	writeRuntimeSettingsIfPresent(t, filepath.Join(configDir, "settings.json"), settings.User)
	writeRuntimeSettingsIfPresent(t, filepath.Join(projectDir, ".claude", "settings.json"), settings.Project)
	writeRuntimeSettingsIfPresent(t, filepath.Join(projectDir, ".claude", "settings.local.json"), settings.Local)

	return configDir, projectDir
}

func prepareRuntimeAuth(t *testing.T, configDir string) {
	t.Helper()

	if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" ||
		strings.TrimSpace(os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")) != "" {
		return
	}

	authSrc := filepath.Join(os.Getenv("HOME"), ".claude.json")
	info, err := os.Stat(authSrc)
	if err == nil && !info.IsDir() {
		authDst := filepath.Join(configDir, ".claude.json")
		if err := copyFile(authSrc, authDst); err != nil {
			t.Fatalf("copy auth config: %v", err)
		}
		if err := os.Chmod(authDst, 0o600); err != nil {
			t.Fatalf("chmod auth config: %v", err)
		}
		return
	}

	t.Fatalf("runtime auth unavailable: set ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN, or ensure %s exists", authSrc)
}

func writeRuntimeSettingsIfPresent(t *testing.T, path string, spec runtimeSettingsSpec) {
	t.Helper()

	if spec.Permissions == nil {
		return
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal settings for %s: %v", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir settings dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write settings %s: %v", path, err)
	}
}

func runClaudeGetSettingsSnapshot(
	t *testing.T,
	ctx context.Context,
	binaryPath string,
	outcome *patchOutcome,
	configDir string,
	projectDir string,
) runtimeSettingsSnapshot {
	t.Helper()

	args := commandArgsForOutcome(outcome, []string{
		binaryPath,
		"--print",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--setting-sources", "user,project,local",
	})
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = projectDir
	cmd.Env = runtimeCommandEnv(configDir)

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
		t.Fatalf("start get_settings process: %v", err)
	}

	lines := []string{
		`{"type":"control_request","request_id":"init-1","request":{"subtype":"initialize"}}`,
		`{"type":"control_request","request_id":"gs-1","request":{"subtype":"get_settings"}}`,
		`{"type":"control_request","request_id":"end-1","request":{"subtype":"end_session"}}`,
	}
	for _, line := range lines {
		if _, err := io.WriteString(stdin, line+"\n"); err != nil {
			t.Fatalf("write control line %q: %v", line, err)
		}
	}
	_ = stdin.Close()

	var snapshot runtimeSettingsSnapshot
	var gotSettings bool
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		var envelope struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string          `json:"subtype"`
				RequestID string          `json:"request_id"`
				Response  json.RawMessage `json:"response"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}
		if envelope.Type != "control_response" || envelope.Response.RequestID != "gs-1" {
			continue
		}
		if err := json.Unmarshal(envelope.Response.Response, &snapshot); err != nil {
			t.Fatalf("decode get_settings response: %v\nline: %s", err, line)
		}
		gotSettings = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan get_settings stdout: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait get_settings process: %v\nstderr:\n%s", err, stderr.String())
	}
	if !gotSettings {
		t.Fatalf("did not receive get_settings response\nstderr:\n%s", stderr.String())
	}
	return snapshot
}

func runClaudePromptCase(
	t *testing.T,
	ctx context.Context,
	binaryPath string,
	outcome *patchOutcome,
	configDir string,
	projectDir string,
	tc runtimePromptCase,
) runtimePromptResult {
	return runClaudePromptCaseWithExtraArgs(t, ctx, binaryPath, outcome, configDir, projectDir, nil, tc)
}

func runClaudePromptCaseWithExtraArgs(
	t *testing.T,
	ctx context.Context,
	binaryPath string,
	outcome *patchOutcome,
	configDir string,
	projectDir string,
	extraArgs []string,
	tc runtimePromptCase,
) runtimePromptResult {
	t.Helper()

	baseArgs := []string{
		"--print",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-prompt-tool", "stdio",
		"--tools", "Bash",
		"--setting-sources", "user,project,local",
		"--append-system-prompt", runtimeRuleSystemPrompt,
		"--max-budget-usd", "0.15",
	}
	launchArgs := make([]string, 0, 1+len(extraArgs)+len(baseArgs))
	launchArgs = append(launchArgs, binaryPath)
	launchArgs = append(launchArgs, extraArgs...)
	launchArgs = append(launchArgs, baseArgs...)
	args := commandArgsForOutcome(outcome, launchArgs)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = projectDir
	cmd.Env = runtimeCommandEnv(configDir)

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
	promptLine := string(promptPayload)
	if err := writeLine(promptLine); err != nil {
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

func runtimeCommandEnv(configDir string) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "CLAUDE_CONFIG_DIR="+configDir)
	env = append(env, "CLAUDE_CODE_ENABLE_TELEMETRY=0")
	return env
}

func decodeToolResultContent(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

func runtimeSettingsContainsRule(
	sources []runtimeSettingsSource,
	source string,
	behavior string,
	rule string,
) bool {
	for _, item := range sources {
		if item.Source != source || item.Settings.Permissions == nil {
			continue
		}
		var rules []string
		switch behavior {
		case "allow":
			rules = item.Settings.Permissions.Allow
		case "ask":
			rules = item.Settings.Permissions.Ask
		case "deny":
			rules = item.Settings.Permissions.Deny
		default:
			return false
		}
		for _, candidate := range rules {
			if candidate == rule {
				return true
			}
		}
	}
	return false
}

func assertRuntimePromptCase(t *testing.T, got runtimePromptResult, want runtimePromptCase) {
	t.Helper()

	if got.Result == nil {
		t.Fatalf("expected final result event\nstderr:\n%s\nstdout:\n%s", got.Stderr, strings.Join(got.RawLines, "\n"))
	}

	if want.ExpectPrompt {
		if len(got.PermissionRequests) == 0 {
			t.Fatalf("expected permission prompt, got none\nstderr:\n%s\nstdout:\n%s", got.Stderr, strings.Join(got.RawLines, "\n"))
		}
	} else if len(got.PermissionRequests) > 0 {
		t.Fatalf("expected no permission prompt, got %+v\nstderr:\n%s\nstdout:\n%s", got.PermissionRequests, got.Stderr, strings.Join(got.RawLines, "\n"))
	}

	if want.ExpectedToolRule != "" {
		foundToolUse := false
		for _, toolUse := range got.ToolUses {
			if toolUse.Name == "Bash" && toolUse.Command == want.ExpectedToolRule {
				foundToolUse = true
				break
			}
		}
		if !foundToolUse {
			t.Fatalf("expected Bash tool_use for %q, got %+v\nstderr:\n%s\nstdout:\n%s", want.ExpectedToolRule, got.ToolUses, got.Stderr, strings.Join(got.RawLines, "\n"))
		}
	}

	if want.ExpectDenied {
		if len(got.Result.PermissionDenials) == 0 {
			t.Fatalf("expected permission denial, got none\nstderr:\n%s\nstdout:\n%s", got.Stderr, strings.Join(got.RawLines, "\n"))
		}
		for _, toolResult := range got.ToolResults {
			if !toolResult.IsError && want.ExpectedStdoutSub != "" && strings.Contains(toolResult.Content, want.ExpectedStdoutSub) {
				t.Fatalf("unexpected successful tool result %q for denied case\nstderr:\n%s\nstdout:\n%s", toolResult.Content, got.Stderr, strings.Join(got.RawLines, "\n"))
			}
		}
		return
	}

	foundToolResult := false
	for _, toolResult := range got.ToolResults {
		if toolResult.IsError {
			continue
		}
		if want.ExpectedStdoutSub == "" || strings.Contains(toolResult.Content, want.ExpectedStdoutSub) {
			foundToolResult = true
			break
		}
	}
	if !foundToolResult {
		t.Fatalf("expected successful tool result containing %q, got %+v\nstderr:\n%s\nstdout:\n%s", want.ExpectedStdoutSub, got.ToolResults, got.Stderr, strings.Join(got.RawLines, "\n"))
	}
}
