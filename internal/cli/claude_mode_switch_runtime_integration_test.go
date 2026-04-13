package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type runtimeLaunchMode struct {
	Name                       string
	BinaryPath                 string
	Outcome                    *patchOutcome
	ExtraArgs                  []string
	ExpectedInitPermissionMode string
}

func TestClaudeModeSwitchRuntimeMatrix(t *testing.T) {
	if os.Getenv(claudeRulesRuntimeTestEnv) != "1" {
		t.Skipf("set %s=1 to run live Claude mode-switch integration tests", claudeRulesRuntimeTestEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	tmpDir := t.TempDir()
	baseBinary := resolveClaudeBinaryForRuntimeTest(t, ctx, tmpDir)
	modes := prepareRuntimeLaunchModes(t, tmpDir, baseBinary)

	for _, sourceMode := range modes {
		sourceMode := sourceMode
		for _, targetMode := range modes {
			targetMode := targetMode
			caseName := fmt.Sprintf("%s_to_%s", sourceMode.Name, targetMode.Name)
			t.Run(caseName, func(t *testing.T) {
				initToken := runtimeModeMatrixToken("INIT", sourceMode.Name, targetMode.Name)
				resumeToken := runtimeModeMatrixToken("RESUME", sourceMode.Name, targetMode.Name)
				initCommand := "printf " + initToken
				resumeCommand := "printf " + resumeToken

				configDir, projectDir := newRuntimeEnvDirs(t, tmpDir, runtimePromptSettings{
					User: runtimeSettingsSpec{
						Permissions: &runtimePermissionsSpec{
							Allow: []string{"Bash(" + initCommand + ")"},
						},
					},
				})

				initial := runClaudePromptCaseWithExtraArgs(
					t,
					ctx,
					sourceMode.BinaryPath,
					sourceMode.Outcome,
					configDir,
					projectDir,
					sourceMode.ExtraArgs,
					runtimePromptCase{
						Name:              "initial-" + caseName,
						Command:           initCommand,
						ExpectPrompt:      false,
						ExpectedToolRule:  initCommand,
						ExpectedStdoutSub: initToken,
					},
				)
				requireRuntimeSessionID(t, initial, "initial-"+caseName)
				requireRuntimePermissionMode(t, initial, sourceMode.ExpectedInitPermissionMode, "initial-"+caseName)

				writeRuntimeSettingsIfPresent(t, filepath.Join(configDir, "settings.json"), runtimeSettingsSpec{
					Permissions: &runtimePermissionsSpec{
						Allow: []string{"Bash(" + resumeCommand + ")"},
					},
				})

				resumeArgs := append([]string{}, targetMode.ExtraArgs...)
				resumeArgs = append(resumeArgs, "--resume", initial.SessionID)
				resumed := runClaudePromptCaseWithExtraArgs(
					t,
					ctx,
					targetMode.BinaryPath,
					targetMode.Outcome,
					configDir,
					projectDir,
					resumeArgs,
					runtimePromptCase{
						Name:              "resumed-" + caseName,
						Command:           resumeCommand,
						ExpectPrompt:      false,
						ExpectedToolRule:  resumeCommand,
						ExpectedStdoutSub: resumeToken,
					},
				)
				requireRuntimeSessionID(t, resumed, "resumed-"+caseName)
				requireRuntimePermissionMode(t, resumed, targetMode.ExpectedInitPermissionMode, "resumed-"+caseName)
				if resumed.SessionID != initial.SessionID {
					t.Fatalf("expected resumed session id %q, got %q", initial.SessionID, resumed.SessionID)
				}
			})
		}
	}
}

func prepareRuntimeLaunchModes(t *testing.T, tmpDir string, baseBinary string) []runtimeLaunchMode {
	t.Helper()

	defaultBinary := copyRuntimeBinaryForMode(t, baseBinary, filepath.Join(tmpDir, "claude-default", claudeBinaryName()))
	bypassBinary := copyRuntimeBinaryForMode(t, baseBinary, filepath.Join(tmpDir, "claude-bypass", claudeBinaryName()))
	rulesBinary := copyRuntimeBinaryForMode(t, baseBinary, filepath.Join(tmpDir, "claude-rules", claudeBinaryName()))

	bypassConfigPath := filepath.Join(tmpDir, "bypass-helper-config.json")
	yoloArgs := resolveYoloBypassArgs(bypassBinary, bypassConfigPath)
	if len(yoloArgs) == 0 {
		t.Skip("installed Claude build does not expose yolo bypass flags")
	}

	var bypassPatchLog bytes.Buffer
	bypassOutcome, err := maybePatchExecutable(append([]string{bypassBinary}, yoloArgs...), exePatchOptions{
		enabledFlag:     true,
		policySettings:  true,
		glibcCompat:     false,
		glibcCompatRoot: "",
	}, bypassConfigPath, &bypassPatchLog)
	if err != nil {
		t.Fatalf("patch copied claude for bypass mode: %v\n%s", err, bypassPatchLog.String())
	}
	if bypassOutcome == nil || !bypassOutcome.BuiltInClaudePatchActive {
		t.Fatalf("expected active built-in Claude patch for bypass mode, got %#v\n%s", bypassOutcome, bypassPatchLog.String())
	}

	var rulesPatchLog bytes.Buffer
	rulesOutcome := patchClaudeCopyForRulesMode(
		t,
		rulesBinary,
		filepath.Join(tmpDir, "rules-helper-config.json"),
		&rulesPatchLog,
	)

	return []runtimeLaunchMode{
		{
			Name:                       "default",
			BinaryPath:                 defaultBinary,
			ExpectedInitPermissionMode: "default",
		},
		{
			Name:                       "yolo_bypass",
			BinaryPath:                 bypassBinary,
			Outcome:                    bypassOutcome,
			ExtraArgs:                  append([]string{}, yoloArgs...),
			ExpectedInitPermissionMode: "bypassPermissions",
		},
		{
			Name:                       "yolo_rule",
			BinaryPath:                 rulesBinary,
			Outcome:                    rulesOutcome,
			ExpectedInitPermissionMode: "default",
		},
	}
}

func copyRuntimeBinaryForMode(t *testing.T, src string, dst string) string {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir runtime mode dir: %v", err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copy runtime binary: %v", err)
	}
	if err := ensureExecutable(dst); err != nil {
		t.Fatalf("ensure runtime binary executable: %v", err)
	}
	return dst
}

func runtimeModeMatrixToken(stage string, sourceMode string, targetMode string) string {
	return strings.ToUpper(strings.Join([]string{
		"MATRIX",
		stage,
		sourceMode,
		targetMode,
		fmt.Sprintf("%d", time.Now().UnixNano()),
	}, "_"))
}

func requireRuntimeSessionID(t *testing.T, result runtimePromptResult, label string) {
	t.Helper()

	if strings.TrimSpace(result.SessionID) != "" {
		return
	}
	t.Fatalf("expected %s to expose session id\nstderr:\n%s\nstdout:\n%s", label, result.Stderr, strings.Join(result.RawLines, "\n"))
}

func requireRuntimePermissionMode(t *testing.T, result runtimePromptResult, want string, label string) {
	t.Helper()

	if got := strings.TrimSpace(result.InitPermissionMode); got != want {
		t.Fatalf("expected %s permission mode %q, got %q\nstderr:\n%s\nstdout:\n%s", label, want, got, result.Stderr, strings.Join(result.RawLines, "\n"))
	}
}
