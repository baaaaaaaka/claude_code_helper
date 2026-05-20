package cli

import (
	"bytes"
	"regexp"
	"testing"
)

func TestPolicySettingsDisablePatch_ReplacesGetterBlock(t *testing.T) {
	requireExePatchEnabled(t)
	startRe := regexp.MustCompile(policySettingsGetterStage1)
	input := []byte("function FI(H){if(H===\"policySettings\"){let L=sqA();if(L)return L}let $=L4(H);return $}")

	out, stats, err := applyPolicySettingsDisablePatch(input, startRe, nil, false)
	if err != nil {
		t.Fatalf("applyPolicySettingsDisablePatch error: %v", err)
	}
	if len(out) != len(input) {
		t.Fatalf("expected output length %d, got %d", len(input), len(out))
	}
	if !bytes.Contains(out, []byte("if(H===\"policySettings\"){return null;")) {
		t.Fatalf("expected policySettings block to be replaced")
	}
	if stats.Segments != 1 || stats.Eligible != 1 || stats.Replacements != 1 || stats.Changed != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestPolicySettingsDisablePatch_ReplacesDirectReturnGetter(t *testing.T) {
	requireExePatchEnabled(t)
	startRe := regexp.MustCompile(policySettingsGetterStage1)
	input := []byte("function f5q(H){if(H===\"policySettings\")return Y5q().settings;let $=VO(H),{settings:q}=$?si($):{settings:null};return q}")

	out, stats, err := applyPolicySettingsDisablePatch(input, startRe, nil, false)
	if err != nil {
		t.Fatalf("applyPolicySettingsDisablePatch error: %v", err)
	}
	if len(out) != len(input) {
		t.Fatalf("expected output length %d, got %d", len(input), len(out))
	}
	if !bytes.Contains(out, []byte("if(H===\"policySettings\")return null")) {
		t.Fatalf("expected policySettings direct return to be replaced")
	}
	if bytes.Contains(out, []byte("return Y5q().settings")) {
		t.Fatalf("expected original policySettings loader to be replaced")
	}
	if stats.Segments != 1 || stats.Eligible != 1 || stats.Replacements != 1 || stats.Changed != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	out2, stats2, err := applyPolicySettingsDisablePatch(out, startRe, nil, false)
	if err != nil {
		t.Fatalf("reapply applyPolicySettingsDisablePatch error: %v", err)
	}
	if !bytes.Equal(out2, out) {
		t.Fatalf("expected reapply to keep output unchanged")
	}
	if stats2.Segments != 1 || stats2.Eligible != 1 || stats2.Replacements != 1 || stats2.Changed != 0 {
		t.Fatalf("unexpected reapply stats: %+v", stats2)
	}
}

func TestPolicySettingsDisablePatch_ReplacesDirectReturnGetterWithMultipleParams(t *testing.T) {
	requireExePatchEnabled(t)
	startRe := regexp.MustCompile(policySettingsGetterStage1)
	input := []byte("function tC8(H,$){if(H===\"policySettings\")return zfq($).settings;let q=sY(H),{settings:K}=q?U_H(q,$):{settings:null};return K}")

	out, stats, err := applyPolicySettingsDisablePatch(input, startRe, nil, false)
	if err != nil {
		t.Fatalf("applyPolicySettingsDisablePatch error: %v", err)
	}
	if len(out) != len(input) {
		t.Fatalf("expected output length %d, got %d", len(input), len(out))
	}
	if !bytes.Contains(out, []byte("if(H===\"policySettings\")return null")) {
		t.Fatalf("expected policySettings direct return to be replaced")
	}
	if bytes.Contains(out, []byte("return zfq($).settings")) {
		t.Fatalf("expected original policySettings loader to be replaced")
	}
	if stats.Segments != 1 || stats.Eligible != 1 || stats.Replacements != 1 || stats.Changed != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestPolicySettingsDisablePatch_NoMatch(t *testing.T) {
	requireExePatchEnabled(t)
	startRe := regexp.MustCompile(policySettingsGetterStage1)
	input := []byte("function other(){return 1}")

	out, stats, err := applyPolicySettingsDisablePatch(input, startRe, nil, false)
	if err != nil {
		t.Fatalf("applyPolicySettingsDisablePatch error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("expected output to be unchanged")
	}
	if stats.Segments != 0 || stats.Eligible != 0 || stats.Changed != 0 || stats.Replacements != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestBypassPermissionsGatePatch_ReplacesKeys(t *testing.T) {
	requireExePatchEnabled(t)
	input := []byte("rf(\"tengu_disable_bypass_permissions_mode\");" +
		"settings.permissions.disableBypassPermissionsMode=\"disable\";")

	out, stats, err := applyBypassPermissionsGatePatch(input, nil, false)
	if err != nil {
		t.Fatalf("applyBypassPermissionsGatePatch error: %v", err)
	}
	if len(out) != len(input) {
		t.Fatalf("expected output length %d, got %d", len(input), len(out))
	}
	if bytes.Contains(out, []byte(bypassPermissionsGateName)) {
		t.Fatalf("expected Statsig gate name to be replaced")
	}
	if bytes.Contains(out, []byte(bypassPermissionsSettingKey)) {
		t.Fatalf("expected settings key to be replaced")
	}
	if stats.Replacements != 2 || stats.Changed == 0 || stats.Segments != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestBypassPermissionsGatePatch_NoMatch(t *testing.T) {
	requireExePatchEnabled(t)
	input := []byte("rf(\"unrelated_gate\")")

	out, stats, err := applyBypassPermissionsGatePatch(input, nil, false)
	if err != nil {
		t.Fatalf("applyBypassPermissionsGatePatch error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("expected output to be unchanged")
	}
	if stats.Segments != 0 || stats.Eligible != 0 || stats.Changed != 0 || stats.Replacements != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestBypassPermissionDecisionPatch_ForcesBypassAllow(t *testing.T) {
	requireExePatchEnabled(t)
	input := permissionDecisionPatchFixture()

	out, stats, err := applyBypassPermissionDecisionPatch(input, nil, false)
	if err != nil {
		t.Fatalf("applyBypassPermissionDecisionPatch error: %v", err)
	}
	if len(out) != len(input) {
		t.Fatalf("expected output length %d, got %d", len(input), len(out))
	}
	if !bytes.Contains(out, []byte(permissionDecisionPatchMarker)) {
		t.Fatalf("expected patched marker")
	}
	if !bytes.Contains(out, []byte(`toolPermissionContext.mode==="bypassPermissions"`)) {
		t.Fatalf("expected bypass mode short circuit")
	}
	if bytes.Contains(out, []byte(permissionDecisionAskRuleAnchor)) {
		t.Fatalf("expected ask-rule prompt path to be replaced")
	}
	if stats.Segments != 1 || stats.Eligible != 1 || stats.Replacements != 1 || stats.Changed != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	out2, stats2, err := applyBypassPermissionDecisionPatch(out, nil, false)
	if err != nil {
		t.Fatalf("reapply applyBypassPermissionDecisionPatch error: %v", err)
	}
	if !bytes.Equal(out2, out) {
		t.Fatalf("expected reapply to keep output unchanged")
	}
	if stats2.Segments != 1 || stats2.Eligible != 1 || stats2.Replacements != 1 || stats2.Changed != 0 {
		t.Fatalf("unexpected reapply stats: %+v", stats2)
	}
}

func TestBypassPermissionDecisionPatch_ReapplyWithResidualAnchorUsesMarker(t *testing.T) {
	requireExePatchEnabled(t)
	patched, _, err := applyBypassPermissionDecisionPatch(permissionDecisionPatchFixture(), nil, false)
	if err != nil {
		t.Fatalf("applyBypassPermissionDecisionPatch error: %v", err)
	}
	withResidualAnchor := append(append([]byte{}, patched...), []byte(`const stringTable="ask rule/safety check requires full permission pipeline";`)...)

	out, stats, err := applyBypassPermissionDecisionPatch(withResidualAnchor, nil, false)
	if err != nil {
		t.Fatalf("reapply applyBypassPermissionDecisionPatch error: %v", err)
	}
	if !bytes.Equal(out, withResidualAnchor) {
		t.Fatalf("expected reapply to keep output unchanged")
	}
	if stats.Eligible == 0 || stats.Replacements == 0 || stats.Changed != 0 {
		t.Fatalf("expected marker to report already-patched state, got %+v", stats)
	}
}

func TestBypassPermissionDecisionPatch_UsesRuleCheckNearestAskAnchor(t *testing.T) {
	requireExePatchEnabled(t)
	input := bytes.Replace(
		permissionDecisionPatchFixture(),
		[]byte("let D=await uDH($,M,K);"),
		[]byte("let P=await preflight($,M,K);let D=await uDH($,M,K);"),
		1,
	)

	out, _, err := applyBypassPermissionDecisionPatch(input, nil, false)
	if err != nil {
		t.Fatalf("applyBypassPermissionDecisionPatch error: %v", err)
	}
	if !bytes.Contains(out, []byte("await uDH(")) {
		t.Fatalf("expected replacement to keep the ask-rule checker")
	}
	if bytes.Contains(out, []byte("await preflight(")) {
		t.Fatalf("expected replacement not to use earlier unrelated await")
	}
}

func TestBypassPermissionDecisionPatch_NoMatch(t *testing.T) {
	requireExePatchEnabled(t)
	input := []byte("async function other(){return 1}")

	out, stats, err := applyBypassPermissionDecisionPatch(input, nil, false)
	if err != nil {
		t.Fatalf("applyBypassPermissionDecisionPatch error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("expected output to be unchanged")
	}
	if stats.Segments != 0 || stats.Eligible != 0 || stats.Changed != 0 || stats.Replacements != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func permissionDecisionPatchFixture() []byte {
	return []byte("async function oO8(H,$,q,K,_,A,z){let Y=$.requiresUserInteraction?.(),f=K.requireCanUseTool;if(H?.behavior===\"deny\")return N(`Hook denied tool use for ${$.name}`),{decision:H,input:q};if(H?.behavior!==\"allow\"&&H?.behavior!==\"ask\")return{decision:await _($,q,K,A,z),input:q};let O=H.behavior,M=H.updatedInput??q,w=Y&&H.updatedInput!==void 0;if(O===\"allow\"&&(Y&&!w||f))return N(`Hook approved tool use for ${$.name}, but canUseTool is required`),{decision:await _($,M,K,A,z),input:M};let D=await uDH($,M,K);if(D?.behavior===\"deny\")return N(`Hook returned '${O}' for ${$.name}, but deny rule overrides: ${D.message}`),{decision:D,input:M};if(D?.behavior===\"ask\")return N(`Hook returned '${O}' for ${$.name}, but ask rule/safety check requires full permission pipeline`),{decision:await _($,M,K,A,z),input:M};if(O===\"allow\")return N(w?`Hook satisfied user interaction for ${$.name} via updatedInput`:`Hook approved tool use for ${$.name}, bypassing permission prompt`),{decision:H,input:M};return{decision:await _($,M,K,A,z,H),input:M}}async function*aO8(){}")
}

func TestRemoteSettingsDisablePatch_ReplacesPaths(t *testing.T) {
	requireExePatchEnabled(t)
	input := []byte("remote-settings.json -- /api/claude_code/settings")

	out, stats, err := applyRemoteSettingsDisablePatch(input, nil, false)
	if err != nil {
		t.Fatalf("applyRemoteSettingsDisablePatch error: %v", err)
	}
	if len(out) != len(input) {
		t.Fatalf("expected output length %d, got %d", len(input), len(out))
	}
	if bytes.Contains(out, []byte(remoteSettingsFileName)) {
		t.Fatalf("expected remote settings file name to be replaced")
	}
	if bytes.Contains(out, []byte(remoteSettingsAPIPath)) {
		t.Fatalf("expected remote settings API path to be replaced")
	}
	if stats.Replacements != 2 || stats.Changed == 0 || stats.Segments != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestRemoteSettingsDisablePatch_NoMatch(t *testing.T) {
	requireExePatchEnabled(t)
	input := []byte("no remote settings here")

	out, stats, err := applyRemoteSettingsDisablePatch(input, nil, false)
	if err != nil {
		t.Fatalf("applyRemoteSettingsDisablePatch error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("expected output to be unchanged")
	}
	if stats.Segments != 0 || stats.Eligible != 0 || stats.Changed != 0 || stats.Replacements != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestRootBypassGuardPatch_ReplacesCondition(t *testing.T) {
	requireExePatchEnabled(t)
	input := []byte("if(" + rootBypassGuardCond + "){console.error(\"" + rootBypassGuardErrorMessage + "\");process.exit(1)}")

	out, stats, err := applyRootBypassGuardPatch(input, nil, false)
	if err != nil {
		t.Fatalf("applyRootBypassGuardPatch error: %v", err)
	}
	if len(out) != len(input) {
		t.Fatalf("expected output length %d, got %d", len(input), len(out))
	}
	if bytes.Contains(out, []byte(rootBypassGuardCond)) {
		t.Fatalf("expected root bypass guard condition to be replaced")
	}
	if !bytes.Contains(out, []byte(rootBypassGuardCondPatched)) {
		t.Fatalf("expected patched root bypass guard condition")
	}
	if stats.Replacements != 1 || stats.Changed != 1 || stats.Segments != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestRootBypassGuardPatch_NoMatch(t *testing.T) {
	requireExePatchEnabled(t)
	input := []byte("if(process.getuid()===0){process.exit(1)}")

	out, stats, err := applyRootBypassGuardPatch(input, nil, false)
	if err != nil {
		t.Fatalf("applyRootBypassGuardPatch error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("expected output to be unchanged")
	}
	if stats.Segments != 0 || stats.Eligible != 0 || stats.Changed != 0 || stats.Replacements != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestRootBypassGuardPatch_ContextFilter(t *testing.T) {
	requireExePatchEnabled(t)
	input := []byte(
		"if(" + rootBypassGuardCond + "){process.exit(1)}\n" +
			"if(" + rootBypassGuardCond + "){console.error(\"" + rootBypassGuardErrorMessage + "\");process.exit(1)}",
	)

	out, stats, err := applyRootBypassGuardPatch(input, nil, false)
	if err != nil {
		t.Fatalf("applyRootBypassGuardPatch error: %v", err)
	}
	if stats.Segments != 2 || stats.Eligible != 1 || stats.Changed != 1 || stats.Replacements != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	if bytes.Count(out, []byte(rootBypassGuardCond)) != 1 {
		t.Fatalf("expected exactly one original condition to remain")
	}
	if bytes.Count(out, []byte(rootBypassGuardCondPatched)) != 1 {
		t.Fatalf("expected exactly one patched condition")
	}
}

func TestRootBypassGuardPatch_NewUpstreamFormat(t *testing.T) {
	requireExePatchEnabled(t)
	// Claude 2.1.86+ refactored the BUBBLEWRAP check from a direct string
	// comparison to a helper function. The shortened pattern must still match.
	input := []byte(
		`if(typeof process.getuid==="function"&&` +
			rootBypassGuardCond +
			`&&!lH(process.env.CLAUDE_CODE_BUBBLEWRAP))console.error("` +
			rootBypassGuardErrorMessage +
			`"),process.exit(1)`,
	)

	out, stats, err := applyRootBypassGuardPatch(input, nil, false)
	if err != nil {
		t.Fatalf("applyRootBypassGuardPatch error: %v", err)
	}
	if stats.Eligible != 1 || stats.Changed != 1 {
		t.Fatalf("expected patch to match new upstream format: %+v", stats)
	}
	if bytes.Contains(out, []byte(rootBypassGuardCond)) {
		t.Fatalf("expected original condition to be replaced")
	}
	if !bytes.Contains(out, []byte(rootBypassGuardCondPatched)) {
		t.Fatalf("expected patched condition")
	}
	// The surrounding code (typeof check, lH() call) must be preserved.
	if !bytes.Contains(out, []byte(`typeof process.getuid==="function"`)) {
		t.Fatalf("expected typeof guard to be preserved")
	}
	if !bytes.Contains(out, []byte(`!lH(process.env.CLAUDE_CODE_BUBBLEWRAP)`)) {
		t.Fatalf("expected lH() call to be preserved")
	}
}

func TestPolicyPatchHelpers(t *testing.T) {
	t.Run("findBlock handles nested braces", func(t *testing.T) {
		data := []byte("function x(){if(true){return '{';}// comment { }\nreturn 1;}")
		open := bytes.IndexByte(data, '{')
		if open < 0 {
			t.Fatalf("expected opening brace")
		}
		start, end, ok := findBlock(data, open)
		if !ok {
			t.Fatalf("expected to find block")
		}
		if start != open || end != len(data) {
			t.Fatalf("expected full function block, got %d:%d", start, end)
		}
	})

	t.Run("findBlock rejects malformed input", func(t *testing.T) {
		data := []byte("{ if(true){")
		start, end, ok := findBlock(data, 0)
		if ok || start != 0 || end != 0 {
			t.Fatalf("expected malformed block to fail, got %v %d:%d", ok, start, end)
		}
	})

	t.Run("looksLikePolicyTextBlock enforces threshold", func(t *testing.T) {
		good := append(bytes.Repeat([]byte("a"), 90), bytes.Repeat([]byte{0x01}, 10)...)
		if !looksLikePolicyTextBlock(good) {
			t.Fatalf("expected threshold to allow 10%% non-printable bytes")
		}
		bad := append(bytes.Repeat([]byte("a"), 89), bytes.Repeat([]byte{0x01}, 11)...)
		if looksLikePolicyTextBlock(bad) {
			t.Fatalf("expected threshold to reject >10%% non-printable bytes")
		}
	})

	t.Run("applyPolicySettingsDisablePatch handles multiple matches", func(t *testing.T) {
		requireExePatchEnabled(t)
		startRe := regexp.MustCompile(policySettingsGetterStage1)
		input := []byte("function A(H){if(H===\"policySettings\"){let L=sqA();if(L)return L}let $=L4(H);return $}" +
			"function B(H){if(\"policySettings\"===H){let L=sqA();if(L)return L}let $=L4(H);return $}")
		out, stats, err := applyPolicySettingsDisablePatch(input, startRe, nil, false)
		if err != nil {
			t.Fatalf("applyPolicySettingsDisablePatch error: %v", err)
		}
		if stats.Segments != 2 || stats.Eligible != 2 || stats.Replacements != 2 {
			t.Fatalf("unexpected stats: %+v", stats)
		}
		if count := bytes.Count(out, []byte("return null;")); count != 2 {
			t.Fatalf("expected 2 replacements, got %d", count)
		}
	})
}
