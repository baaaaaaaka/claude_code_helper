package cli

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuiltInPolicyPatchLeavesPermissionRuleStringsUntouched(t *testing.T) {
	requireExePatchEnabled(t)

	specs, err := policySettingsSpecs()
	if err != nil {
		t.Fatalf("policySettingsSpecs error: %v", err)
	}

	input := []byte(`const localSettings={"permissions":{"allow":["Bash(ls:*)"],"ask":["Bash(rm:*)"],"deny":["PowerShell(Invoke-Expression:*)"]}};
const managedPolicy={"allowManagedPermissionRulesOnly":true,"permissions":{"ask":["Bash(*)"]}};
function FI(H){if(H==="policySettings"){let L=sqA();if(L&&Object.keys(L).length>0)return L}let $=L4(H);if(!$)return null;let{settings:A}=DmA($);return A}
const gate="tengu_disable_bypass_permissions_mode";
const key="disableBypassPermissionsMode";
if(process.getuid()===0&&process.env.IS_SANDBOX!=="1"){throw new Error("--dangerously-skip-permissions cannot be used with root/sudo privileges for security reasons")}
const remoteFile="remote-settings.json";
const remotePath="/api/claude_code/settings";`)

	out, stats, err := applyExePatches(input, specs, io.Discard, false)
	if err != nil {
		t.Fatalf("applyExePatches error: %v", err)
	}
	if len(stats) != 4 {
		t.Fatalf("expected 4 built-in policy patch stats, got %d", len(stats))
	}
	got := string(out)

	for _, rule := range []string{
		`"Bash(ls:*)"`,
		`"Bash(rm:*)"`,
		`"PowerShell(Invoke-Expression:*)"`,
		`"allowManagedPermissionRulesOnly":true`,
	} {
		if !strings.Contains(got, rule) {
			t.Fatalf("expected patched output to keep rule string %s", rule)
		}
	}

	for _, before := range []string{
		`tengu_disable_bypass_permissions_mode`,
		`disableBypassPermissionsMode`,
		`process.getuid()===0&&process.env.IS_SANDBOX!=="1"`,
		`remote-settings.json`,
		`/api/claude_code/settings`,
	} {
		if strings.Contains(got, before) {
			t.Fatalf("expected patched output to replace %q", before)
		}
	}
	if !strings.Contains(got, `return null;`) {
		t.Fatalf("expected patched output to disable policySettings getter")
	}
}

func TestClaudeCodeSourceContractPermissionRuleShapes(t *testing.T) {
	parser := readClaudeCodeSource(t, "src/utils/permissions/permissionRuleParser.ts")
	matching := readClaudeCodeSource(t, "src/utils/permissions/shellRuleMatching.ts")

	if !strings.Contains(parser, `Format: "ToolName" or "ToolName(content)"`) {
		t.Fatalf("expected permissionRuleParser to document tool/content rule shape")
	}
	if !strings.Contains(parser, `rawContent === '' || rawContent === '*'`) {
		t.Fatalf("expected tool-wide Bash(*) normalization to remain in permissionRuleParser")
	}

	assertContainsAll(t, matching,
		`type: 'exact'`,
		`type: 'prefix'`,
		`type: 'wildcard'`,
		`permissionRule.match(/^(.+):\*$/)`,
		`if (hasWildcards(permissionRule))`,
		`destination: 'localSettings'`,
	)
}

func TestClaudeCodeSourceContractRulePipelineOrdering(t *testing.T) {
	permissions := readClaudeCodeSource(t, "src/utils/permissions/permissions.ts")
	bash := readClaudeCodeSource(t, "src/tools/BashTool/bashPermissions.ts")

	assertContainsInOrder(t, permissions,
		`Tool implementation denied permission`,
		`Content-specific ask rules from tool.checkPermissions take precedence`,
		`Safety checks (e.g. .git/, .claude/, .vscode/, shell configs) are`,
		`const shouldBypassPermissions =`,
	)

	assertContainsInOrder(t, bash,
		`Return immediately if there's an explicit deny rule on the full command`,
		`Subcommand deny checks must run BEFORE full-command ask returns.`,
		`Full-command ask check (after all deny sources have been exhausted)`,
	)

	assertContainsAll(t, bash,
		`const matchingAskRules = filterRulesByContentsMatchingInput(`,
		`const matchingAllowRules = filterRulesByContentsMatchingInput(`,
		`matchingDenyRules[0] !== undefined`,
		`matchingAskRules[0] !== undefined`,
	)
}

func TestClaudeCodeSourceContractLocalRulesVsManagedRules(t *testing.T) {
	loader := readClaudeCodeSource(t, "src/utils/permissions/permissionsLoader.ts")
	settings := readClaudeCodeSource(t, "src/utils/settings/settings.ts")
	constants := readClaudeCodeSource(t, "src/utils/settings/constants.ts")
	addRules := readClaudeCodeSource(t, "src/components/permissions/rules/AddPermissionRules.tsx")

	assertContainsAll(t, loader,
		`getSettingsForSource('policySettings')?.allowManagedPermissionRulesOnly ===`,
		`return !shouldAllowManagedPermissionRulesOnly()`,
		`return getPermissionRulesForSource('policySettings')`,
		`for (const source of getEnabledSettingSources())`,
		`'userSettings'`,
		`'projectSettings'`,
		`'localSettings'`,
	)

	assertContainsAll(t, constants,
		`'policySettings',`,
		`Editable setting sources (excludes policySettings and flagSettings which are read-only)`,
		`'localSettings',`,
		`'projectSettings',`,
		`'userSettings',`,
	)

	assertContainsAll(t, settings,
		`return join('.claude', 'settings.json')`,
		`return join('.claude', 'settings.local.json')`,
		`return 'settings.json'`,
	)

	assertContainsAll(t, addRules,
		`label: 'Project settings (local)'`,
		`label: 'Project settings'`,
		`label: 'User settings'`,
	)
}

func TestClaudeCodeSourceContractManagedOnlyDisablesPersistenceAndAlwaysAllowUI(t *testing.T) {
	loader := readClaudeCodeSource(t, "src/utils/permissions/permissionsLoader.ts")
	bashOptions := readClaudeCodeSource(t, "src/components/permissions/BashPermissionRequest/bashToolUseOptions.tsx")
	powerShellOptions := readClaudeCodeSource(t, "src/components/permissions/PowerShellPermissionRequest/powershellToolUseOptions.tsx")

	assertContainsInOrder(t, loader,
		`Returns true if "always allow" options should be shown in permission prompts.`,
		`return !shouldAllowManagedPermissionRulesOnly()`,
		`When allowManagedPermissionRulesOnly is enabled, don't persist new permission rules`,
		`if (shouldAllowManagedPermissionRulesOnly()) {`,
		`return false`,
	)

	assertContainsAll(t, bashOptions,
		`Only show "always allow" options when not restricted by allowManagedPermissionRulesOnly`,
		`if (shouldShowAlwaysAllowOptions()) {`,
	)

	assertContainsAll(t, powerShellOptions,
		`Only show "always allow" options when not restricted by allowManagedPermissionRulesOnly.`,
		`if (shouldShowAlwaysAllowOptions() && suggestions.length > 0) {`,
	)
}

func TestClaudeCodeSourceContractPowerShellRuleBehavior(t *testing.T) {
	powershell := readClaudeCodeSource(t, "src/tools/PowerShellTool/powershellPermissions.ts")

	assertContainsInOrder(t, powershell,
		`PowerShell-specific: uses case-insensitive matching throughout.`,
		`Check deny/ask rules BEFORE parse validity check.`,
		`canonical resolution handles aliases`,
		`if (!trimmedFrag) continue // skip empty fragments`,
	)

	assertContainsAll(t, powershell,
		`const parsed = await parsePowerShellCommand(command)`,
		`return a.toLowerCase() === b.toLowerCase()`,
		`return str.toLowerCase().startsWith(prefix.toLowerCase())`,
	)
}

func TestClaudeCodeSourceContractBashToolIdentityAndShellSelection(t *testing.T) {
	toolName := readClaudeCodeSource(t, "src/tools/BashTool/toolName.ts")
	shell := readClaudeCodeSource(t, "src/utils/Shell.ts")

	assertContainsAll(t, toolName,
		`export const BASH_TOOL_NAME = 'Bash'`,
	)

	assertContainsInOrder(t, shell,
		`const shellOverride = process.env.CLAUDE_CODE_SHELL`,
		`shellOverride.includes('bash') || shellOverride.includes('zsh')`,
		`Only consider SHELL if it's bash or zsh`,
		`const shellOrder = preferBash ? ['bash', 'zsh'] : ['zsh', 'bash']`,
	)
}

func TestClaudeCodeSourceContractPermissionSuggestionDefaults(t *testing.T) {
	matching := readClaudeCodeSource(t, "src/utils/permissions/shellRuleMatching.ts")

	assertContainsAll(t, matching,
		`export function suggestionForExactCommand(`,
		`export function suggestionForPrefix(`,
	)
	assertContainsNTimes(t, matching, `destination: 'localSettings'`, 2)
}

func readClaudeCodeSource(t *testing.T, rel string) string {
	t.Helper()

	rel = filepath.ToSlash(rel)
	for _, root := range claudeCodeSourceRoots(t) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data)
		}
	}

	if fixture, ok := claudeCodeContractFixtures[rel]; ok {
		return fixture
	}

	t.Fatalf("Claude Code source fixture missing for %s", rel)
	return ""
}

func claudeCodeSourceRoots(t *testing.T) []string {
	t.Helper()

	roots := make([]string, 0, 2)
	if root := strings.TrimSpace(os.Getenv("CLAUDE_CODE_SOURCE_DIR")); root != "" {
		roots = append(roots, root)
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("resolve current test file path")
	}
	roots = append(roots, filepath.Join(filepath.Dir(thisFile), "../../../../claude-code"))
	return roots
}

func assertContainsAll(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			t.Fatalf("expected source to contain %q", needle)
		}
	}
}

func assertContainsInOrder(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	prev := -1
	for _, needle := range needles {
		searchFrom := prev + 1
		idx := strings.Index(haystack[searchFrom:], needle)
		if idx < 0 {
			t.Fatalf("expected source to contain %q", needle)
		}
		prev = searchFrom + idx
	}
}

func assertContainsNTimes(t *testing.T, haystack string, needle string, want int) {
	t.Helper()
	got := strings.Count(haystack, needle)
	if got < want {
		t.Fatalf("expected source to contain %q at least %d times, got %d", needle, want, got)
	}
}

var claudeCodeContractFixtures = map[string]string{
	"src/utils/permissions/permissionRuleParser.ts": `
Format: "ToolName" or "ToolName(content)"
permissionRuleValueFromString('Bash')
permissionRuleValueFromString('Bash(npm install)')
rawContent === '' || rawContent === '*'
`,
	"src/utils/permissions/shellRuleMatching.ts": `
type: 'exact'
type: 'prefix'
type: 'wildcard'
permissionRule.match(/^(.+):\*$/)
if (hasWildcards(permissionRule))
export function suggestionForExactCommand(
destination: 'localSettings',
export function suggestionForPrefix(
destination: 'localSettings',
`,
	"src/utils/permissions/permissions.ts": `
const PERMISSION_RULE_SOURCES = [
  ...SETTING_SOURCES,
  'cliArg',
  'command',
  'session',
]
Tool implementation denied permission
Content-specific ask rules from tool.checkPermissions take precedence
Safety checks (e.g. .git/, .claude/, .vscode/, shell configs) are
const shouldBypassPermissions =
When allowManagedPermissionRulesOnly is enabled, clear all non-policy sources
`,
	"src/tools/BashTool/bashPermissions.ts": `
Return immediately if there's an explicit deny rule on the full command
Subcommand deny checks must run BEFORE full-command ask returns.
Full-command ask check (after all deny sources have been exhausted)
const matchingAskRules = filterRulesByContentsMatchingInput(
const matchingAllowRules = filterRulesByContentsMatchingInput(
matchingDenyRules[0] !== undefined
matchingAskRules[0] !== undefined
`,
	"src/utils/permissions/permissionsLoader.ts": `
Returns true if allowManagedPermissionRulesOnly is enabled in managed settings (policySettings).
getSettingsForSource('policySettings')?.allowManagedPermissionRulesOnly ===
Returns true if "always allow" options should be shown in permission prompts.
return !shouldAllowManagedPermissionRulesOnly()
return getPermissionRulesForSource('policySettings')
for (const source of getEnabledSettingSources())
'userSettings'
'projectSettings'
'localSettings'
When allowManagedPermissionRulesOnly is enabled, don't persist new permission rules
if (shouldAllowManagedPermissionRulesOnly()) {
    return false
}
`,
	"src/utils/settings/constants.ts": `
'policySettings',
Editable setting sources (excludes policySettings and flagSettings which are read-only)
'localSettings',
'projectSettings',
'userSettings',
`,
	"src/utils/settings/settings.ts": `
return 'settings.json'
return join('.claude', 'settings.json')
return join('.claude', 'settings.local.json')
`,
	"src/components/permissions/rules/AddPermissionRules.tsx": `
label: 'Project settings (local)'
label: 'Project settings'
label: 'User settings'
`,
	"src/components/permissions/BashPermissionRequest/bashToolUseOptions.tsx": `
Only show "always allow" options when not restricted by allowManagedPermissionRulesOnly
if (shouldShowAlwaysAllowOptions()) {
`,
	"src/components/permissions/PowerShellPermissionRequest/powershellToolUseOptions.tsx": `
Only show "always allow" options when not restricted by allowManagedPermissionRulesOnly.
if (shouldShowAlwaysAllowOptions() && suggestions.length > 0) {
`,
	"src/tools/PowerShellTool/powershellPermissions.ts": `
PowerShell-specific: uses case-insensitive matching throughout.
return a.toLowerCase() === b.toLowerCase()
return str.toLowerCase().startsWith(prefix.toLowerCase())
const parsed = await parsePowerShellCommand(command)
Check deny/ask rules BEFORE parse validity check.
canonical resolution handles aliases
if (!trimmedFrag) continue // skip empty fragments
`,
	"src/tools/BashTool/toolName.ts": `
export const BASH_TOOL_NAME = 'Bash'
`,
	"src/utils/Shell.ts": `
const shellOverride = process.env.CLAUDE_CODE_SHELL
shellOverride.includes('bash') || shellOverride.includes('zsh')
Only consider SHELL if it's bash or zsh
const shellOrder = preferBash ? ['bash', 'zsh'] : ['zsh', 'bash']
`,
}
