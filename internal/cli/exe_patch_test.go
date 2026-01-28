package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestApplyExePatch_MultipleMatches(t *testing.T) {
	requireExePatchEnabled(t)
	spec := exePatchSpec{
		match:   regexp.MustCompile(`abc\d{3}xyz`),
		guard:   regexp.MustCompile(`123`),
		patch:   regexp.MustCompile(`\d{3}`),
		replace: []byte("999"),
	}

	input := []byte("abc123xyz--abc456xyz")
	out, stats, err := applyExePatch(input, spec, nil, false)
	if err != nil {
		t.Fatalf("applyExePatch error: %v", err)
	}

	if !bytes.Equal(out, []byte("abc999xyz--abc456xyz")) {
		t.Fatalf("unexpected output: %q", out)
	}
	if stats.Segments != 2 || stats.Eligible != 1 || stats.Patched != 1 || stats.Replacements != 1 || stats.Changed != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestApplyExePatch_GuardBlocksPatch(t *testing.T) {
	requireExePatchEnabled(t)
	spec := exePatchSpec{
		match:   regexp.MustCompile(`abc\d{3}xyz`),
		guard:   regexp.MustCompile(`nope`),
		patch:   regexp.MustCompile(`\d{3}`),
		replace: []byte("999"),
	}

	input := []byte("abc123xyz")
	out, stats, err := applyExePatch(input, spec, nil, false)
	if err != nil {
		t.Fatalf("applyExePatch error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("expected output to be unchanged: %q", out)
	}
	if stats.Segments != 1 || stats.Eligible != 0 || stats.Patched != 0 || stats.Replacements != 0 || stats.Changed != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestApplyExePatch_EmptyStage1Match(t *testing.T) {
	requireExePatchEnabled(t)
	spec := exePatchSpec{
		match:   regexp.MustCompile(`^`),
		guard:   regexp.MustCompile(`.`),
		patch:   regexp.MustCompile(`.`),
		replace: []byte("x"),
	}

	if _, _, err := applyExePatch([]byte("abc"), spec, nil, false); err == nil {
		t.Fatalf("expected error for empty stage-1 match")
	}
}

func TestApplyExePatch_PatchRegexMissingMatch(t *testing.T) {
	requireExePatchEnabled(t)
	spec := exePatchSpec{
		match:   regexp.MustCompile(`abc\d{3}xyz`),
		guard:   regexp.MustCompile(`123`),
		patch:   regexp.MustCompile(`zzz`),
		replace: []byte("999"),
	}

	if _, _, err := applyExePatch([]byte("abc123xyz"), spec, nil, false); err == nil {
		t.Fatalf("expected error when stage-3 regex does not match")
	}
}

func TestPolicySettingsPatchesPreview(t *testing.T) {
	requireExePatchEnabled(t)
	specs, err := policySettingsSpecs()
	if err != nil {
		t.Fatalf("policySettingsSpecs error: %v", err)
	}

	input := []byte("if(a==='policySettings'){let U=XI(\"policySettings\");if(U)$=YnH($,U,zmD);continue}--if(b==='policySettings'){let I=FYD();if(I===null)return null;return I===\"remote\"?\"Enterprise managed settings (remote)\":\"Enterprise managed settings (local)\"}")
	var log bytes.Buffer
	out, stats, err := applyExePatches(input, specs, &log, true)
	if err != nil {
		t.Fatalf("applyExePatches error: %v", err)
	}

	t.Logf("output: %q", out)
	t.Logf("preview output:\n%s", log.String())

	if len(out) != len(input) {
		t.Fatalf("expected output length %d, got %d", len(input), len(out))
	}
	if bytes.Contains(out, []byte("if(U)$=YnH($,U,zmD);")) {
		t.Fatalf("expected continue replacement to remove if(U) statement")
	}
	if bytes.Contains(out, []byte("let I=FYD();")) {
		t.Fatalf("expected return-null replacement to remove let I assignment")
	}
	if !bytes.Contains(out, []byte("return null;if(I===null)")) {
		t.Fatalf("expected return-null replacement to be present")
	}
	if len(stats) != 1 {
		t.Fatalf("expected stats for one spec, got %d", len(stats))
	}
	if !strings.Contains(log.String(), "before=") || !strings.Contains(log.String(), "after=") {
		t.Fatalf("expected preview log output, got: %s", log.String())
	}
}
