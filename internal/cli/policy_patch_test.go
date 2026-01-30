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
