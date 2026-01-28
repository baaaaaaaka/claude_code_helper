package cli

import (
	"bytes"
	"regexp"
	"testing"
)

func TestPolicySettingsBlockPatch_ReplacesStatementBeforeContinue(t *testing.T) {
	requireExePatchEnabled(t)
	startRe := regexp.MustCompile(policySettingsStage1)
	input := []byte("if(a==='policySettings'){x=1;callSomeFunction();continue}")

	out, stats, err := applyPolicySettingsBlockPatch(input, startRe, nil, false)
	if err != nil {
		t.Fatalf("applyPolicySettingsBlockPatch error: %v", err)
	}
	if len(out) != len(input) {
		t.Fatalf("expected output length %d, got %d", len(input), len(out))
	}
	if bytes.Contains(out, []byte("callSomeFunction();")) {
		t.Fatalf("expected callSomeFunction statement to be replaced")
	}
	if !bytes.Contains(out, []byte("continue;")) {
		t.Fatalf("expected continue replacement to be present")
	}
	if stats.Changed == 0 || stats.Replacements != 1 || stats.Eligible != 1 || stats.Segments != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestPolicySettingsBlockPatch_ReplacesStatementBeforeReturnNull(t *testing.T) {
	requireExePatchEnabled(t)
	startRe := regexp.MustCompile(policySettingsStage1)
	input := []byte("if(a==='policySettings'){let I=computeSomething();if(I===null)return null;return I}")

	out, stats, err := applyPolicySettingsBlockPatch(input, startRe, nil, false)
	if err != nil {
		t.Fatalf("applyPolicySettingsBlockPatch error: %v", err)
	}
	if len(out) != len(input) {
		t.Fatalf("expected output length %d, got %d", len(input), len(out))
	}
	if bytes.Contains(out, []byte("let I=computeSomething();")) {
		t.Fatalf("expected let I assignment to be replaced")
	}
	if !bytes.Contains(out, []byte("return null;")) {
		t.Fatalf("expected return null replacement to be present")
	}
	if stats.Changed == 0 || stats.Replacements != 1 || stats.Eligible != 1 || stats.Segments != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestPolicySettingsBlockPatch_SkipsWhenNoEligibleStatement(t *testing.T) {
	requireExePatchEnabled(t)
	startRe := regexp.MustCompile(policySettingsStage1)
	input := []byte("if(a==='policySettings'){x=1;continue}")

	out, stats, err := applyPolicySettingsBlockPatch(input, startRe, nil, false)
	if err != nil {
		t.Fatalf("applyPolicySettingsBlockPatch error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("expected output to be unchanged")
	}
	if stats.Changed != 0 || stats.Replacements != 0 || stats.Eligible != 0 || stats.Segments != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestPolicySettingsBlockPatch_IgnoreStringsAndComments(t *testing.T) {
	requireExePatchEnabled(t)
	startRe := regexp.MustCompile(policySettingsStage1)
	input := []byte("if(a==='policySettings'){const s=\"return null\";/* continue; */}")

	out, stats, err := applyPolicySettingsBlockPatch(input, startRe, nil, false)
	if err != nil {
		t.Fatalf("applyPolicySettingsBlockPatch error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("expected output to be unchanged")
	}
	if stats.Changed != 0 || stats.Replacements != 0 || stats.Eligible != 0 || stats.Segments != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestPolicySettingsBlockPatch_SkipsBinaryLikeBlockWithNUL(t *testing.T) {
	requireExePatchEnabled(t)
	startRe := regexp.MustCompile(policySettingsStage1)
	input := []byte("if(a==='policySettings'){x=1;")
	input = append(input, 0x00)
	input = append(input, []byte("callSomeFunction();continue}")...)

	out, stats, err := applyPolicySettingsBlockPatch(input, startRe, nil, false)
	if err != nil {
		t.Fatalf("applyPolicySettingsBlockPatch error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("expected output to be unchanged")
	}
	if stats.Changed != 0 || stats.Replacements != 0 || stats.Eligible != 0 || stats.Segments != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestPolicySettingsBlockPatch_SkipsBinaryLikeBlockWithNonPrintable(t *testing.T) {
	requireExePatchEnabled(t)
	startRe := regexp.MustCompile(policySettingsStage1)
	input := []byte("if(a==='policySettings'){x=1;")
	input = append(input, bytes.Repeat([]byte{0x01}, 12)...)
	input = append(input, []byte("callSomeFunction();continue}")...)

	out, stats, err := applyPolicySettingsBlockPatch(input, startRe, nil, false)
	if err != nil {
		t.Fatalf("applyPolicySettingsBlockPatch error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("expected output to be unchanged")
	}
	if stats.Changed != 0 || stats.Replacements != 0 || stats.Eligible != 0 || stats.Segments != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}
