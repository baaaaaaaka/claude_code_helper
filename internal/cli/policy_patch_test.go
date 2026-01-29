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
