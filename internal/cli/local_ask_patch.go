package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
)

const (
	localAskPatchLabel   = "bypass-local-ask"
	localAskPatchMarker  = "/*clpAsk2*/"
	bashAskPatchV1Marker = "/*clpAsk*/"
	bunSourceLaunchEnv   = "BUN_JSC_useCodeCache=false"
	localAskPatchAnchor  = `("permission_bypass_removal_prompt_waived");`
	localAskIdentifier   = `[A-Za-z_$][A-Za-z0-9_$]*`
)

var (
	// Capture the tool, its input and context, and the permission-context getter.
	localAskEntryRe = regexp.MustCompile(`^async function ` + localAskIdentifier +
		`\((` + localAskIdentifier + `),(` + localAskIdentifier + `),(` + localAskIdentifier + `),` +
		localAskIdentifier + `=` + localAskIdentifier + `,` + localAskIdentifier +
		`\)\{if\((` + localAskIdentifier + `)\.abortController\.signal\.aborted\)throw new ` +
		localAskIdentifier + `;(?:` + localAskIdentifier + `\(` + localAskIdentifier +
		`\);)?let ` + localAskIdentifier + `=(` + localAskIdentifier + `)\((` + localAskIdentifier + `)\),`)
	// Use space occupied by diagnostic messages to keep the bundled function the
	// same length. The permission decisions around the messages stay in place.
	localAskWaiverLogRe = regexp.MustCompile(`if\(` + localAskIdentifier + `&&` + localAskIdentifier +
		`\(` + localAskIdentifier + `\.decisionReason,` + localAskIdentifier + `\)!==void 0\)` +
		localAskIdentifier + `\("permission_bypass_removal_prompt_waived"\);`)
	localAskComputerLogRe = regexp.MustCompile(`let ` + localAskIdentifier + `=` + localAskIdentifier +
		`\(` + localAskIdentifier + `\)\.servedCall!==!0\?` + localAskIdentifier +
		`\(` + localAskIdentifier + `,` + localAskIdentifier + `,` + localAskIdentifier +
		`\):void 0;if\(` + localAskIdentifier + `!==void 0\)` + localAskIdentifier +
		`\("tengu_auto_mode_computer_use_rule_allow",\{computerUseActionKind:` +
		localAskIdentifier + `\(` + localAskIdentifier + `\),ruleSource:` +
		localAskIdentifier + `\(` + localAskIdentifier + `\.source\)\}\);`)
	localAskDestructiveMCPLogRe = regexp.MustCompile(`let ` + localAskIdentifier + `=` + localAskIdentifier +
		`\(` + localAskIdentifier + `\),` + localAskIdentifier + `=` + localAskIdentifier +
		`\(` + localAskIdentifier + `,` + localAskIdentifier + `,` + localAskIdentifier +
		`,` + localAskIdentifier + `,` + localAskIdentifier + `\);if\(` + localAskIdentifier +
		`!==void 0\)\{if\(!0\)` + localAskIdentifier +
		`\("tengu_auto_mode_destructive_mcp_rule_allow",\{ruleSource:` + localAskIdentifier +
		`\(` + localAskIdentifier + `\),serverClassifierRole:` + localAskIdentifier +
		`\(` + localAskIdentifier + `\(` + localAskIdentifier + `\.mode\)\)\}\)\}`)
	localAskSuggestionsLogRe = regexp.MustCompile(`if\(` + localAskIdentifier +
		`\.behavior==="ask"&&` + localAskIdentifier + `\.suggestions\)` + localAskIdentifier +
		"\\(`Permission suggestions for \\$\\{" + localAskIdentifier + `\.name\}: \$\{` +
		localAskIdentifier + `\(` + localAskIdentifier + "\\.suggestions,null,2\\)\\}`,\\{level:\"verbose\"\\}\\);")
)

func bypassLocalAskPatchSpec() exePatchSpec {
	return exePatchSpec{
		label:       localAskPatchLabel,
		applyID:     localAskPatchLabel + "-v2",
		apply:       applyBypassLocalAskPatch,
		fixedLength: true,
	}
}

func applyBypassLocalAskPatch(data []byte, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
	stats := exePatchStats{Label: localAskPatchLabel}
	if bytes.Contains(data, []byte(localAskPatchMarker)) {
		stats.Segments, stats.Eligible, stats.Patched = 1, 1, 1
		return data, stats, nil
	}
	if bytes.Contains(data, []byte(bashAskPatchV1Marker)) {
		return data, stats, nil
	}
	// Running the bundle from source would also activate an existing hook
	// shortcut. Leave legacy binaries without a clean patch source alone.
	if bytes.Contains(data, []byte(permissionDecisionPatchMarkerV4)) {
		return data, stats, nil
	}

	anchor := []byte(localAskPatchAnchor)
	for offset := 0; offset < len(data); {
		index := bytes.Index(data[offset:], anchor)
		if index < 0 {
			break
		}
		index += offset
		offset = index + len(anchor)
		start, end, ok := findPermissionDecisionFunction(data, index)
		if !ok {
			continue
		}
		segment := data[start:end]
		if !bytes.Contains(segment, []byte("tengu_mcp_server_policy_bypass_exempt")) ||
			!bytes.Contains(segment, []byte(".checkPermissions(")) {
			continue
		}
		stats.Segments++
		patched := patchLocalAskInPermissionDecision(segment)
		if patched == nil {
			continue
		}
		if preview {
			logPatchPreview(log, stats.Label, segment, patched)
		}
		out := bytes.Clone(data)
		copy(out[start:end], patched)
		stats.Eligible, stats.Patched, stats.Changed, stats.Replacements = 1, 1, 1, 1
		return out, stats, nil
	}
	return data, stats, nil
}

func patchLocalAskInPermissionDecision(segment []byte) []byte {
	entry := localAskEntryRe.FindSubmatch(segment)
	if entry == nil || !bytes.Equal(entry[3], entry[4]) || !bytes.Equal(entry[3], entry[6]) {
		return nil
	}
	tool, input, context, getPermissions := entry[1], entry[2], entry[3], entry[5]
	for _, identifier := range [][]byte{tool, input, context} {
		switch string(identifier) {
		case "$x", "$t", "$i":
			return nil
		}
	}

	// Filter ordinary rules for the current tool in this call's permission
	// context. A rule can still target a named input parameter. Bash command
	// rules use the command field itself. Keep ambiguous Bash selectors unless
	// they have the legacy command-prefix suffix.
	setup := fmt.Sprintf(
		`if(/^(Bash|Read|Write|Edit)$/.test(%[1]s.name)&&%[2]s(%[3]s).mode==="bypassPermissions"&&!%[3]s.forRemoteExecution){`+
			`let $x=%[3]s,$t=%[1]s.name,$i=%[5]s;%[3]s={...$x,getAppState(){let s=$x.getAppState(),p=s.toolPermissionContext,a={...p.alwaysAskRules};`+
			`for(let k in a)if(/^(user|local|project|flag)Settings$/.test(k))a[k]=a[k]?.filter(r=>{let n=/^\w+\(\s*([\w$]+)\s*:/.exec(r)?.[1];return r.split("(")[0]!==$t||n&&n!=="command"&&(Object.hasOwn($i??{},n)||$t==="Bash"&&!r.endsWith(":*)"))});`+
			`return{...s,toolPermissionContext:{...p,alwaysAskRules:a}}}}}%[4]s`,
		tool, getPermissions, context, localAskPatchMarker, input,
	)
	// The entry ends at the first permission-context lookup. Insert immediately
	// before it so both the rule lookup and the tool check see the same context.
	insertAt := bytes.LastIndex(entry[0], []byte("let "))
	updated := make([]byte, 0, len(segment)+len(setup))
	updated = append(updated, segment[:insertAt]...)
	updated = append(updated, setup...)
	updated = append(updated, segment[insertAt:]...)
	for _, pattern := range []*regexp.Regexp{localAskWaiverLogRe, localAskComputerLogRe, localAskSuggestionsLogRe} {
		if len(pattern.FindAllIndex(updated, 2)) != 1 {
			return nil
		}
		updated = pattern.ReplaceAll(updated, nil)
	}
	if len(updated) > len(segment) && len(localAskDestructiveMCPLogRe.FindAllIndex(updated, 2)) == 1 {
		updated = localAskDestructiveMCPLogRe.ReplaceAll(updated, nil)
	}
	if len(updated) > len(segment) {
		return nil
	}
	return paddedLiteral(string(updated), len(segment))
}

func configureLocalAskPatchLaunch(outcome *patchOutcome) error {
	// Bun can load embedded bytecode for this function. Read the patched
	// source instead, using an environment override scoped to the launch.
	for _, stat := range outcome.PatchStats {
		if stat.Label == localAskPatchLabel && stat.Eligible > 0 {
			outcome.LaunchEnv = append(outcome.LaunchEnv, bunSourceLaunchEnv)
			return nil
		}
	}
	// On an in-place history hit, the patcher has no statistics for this run.
	if len(outcome.PatchStats) == 0 && outcome.AlreadyPatched {
		data, err := os.ReadFile(outcome.TargetPath)
		if err != nil {
			return fmt.Errorf("inspect local permission patch: %w", err)
		}
		if bytes.Contains(data, []byte(localAskPatchMarker)) {
			outcome.LaunchEnv = append(outcome.LaunchEnv, bunSourceLaunchEnv)
		}
	}
	return nil
}
