package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const localAskPatchFixture = `async function decide(t,i,c,p=route,h){if(c.abortController.signal.aborted)throw new Stop;prepare(c);let y=state(c),d=denials(t,i,c);if(d)return d;
if(remotePolicy("tengu_mcp_server_policy_bypass_exempt"))return checkPolicy(t);
let b=await t.checkPermissions(i,c);if(b.behavior==="deny")return b;
if(b.decisionReason?.type==="safetyCheck")return b;
if(allowed){if(safe&&cause(b.decisionReason,key)!==void 0)log("permission_bypass_removal_prompt_waived");return{behavior:"allow",updatedInput:i}}
if(r){let vt=state(c).servedCall!==!0?computer(t,i,p):void 0;if(vt!==void 0)log("tengu_auto_mode_computer_use_rule_allow",{computerUseActionKind:field(vt),ruleSource:field(r.source)});let ct=state(c),mt=mcp(r,t,i,c,ct);if(mt!==void 0){if(!0)log("tengu_auto_mode_destructive_mcp_rule_allow",{ruleSource:field(mt),serverClassifierRole:field(convert(ct.mode))})}return{behavior:"allow",updatedInput:i}}
if(b.behavior==="ask"&&b.suggestions)log(` + "`Permission suggestions for ${t.name}: ${fmt(b.suggestions,null,2)}`" + `,{level:"verbose"});return b}`

func TestBypassLocalAskPatch(t *testing.T) {
	input := []byte(localAskPatchFixture + "function next(){}")
	patched, stats, err := applyBypassLocalAskPatch(input, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed != 1 || stats.Eligible != 1 || len(patched) != len(input) {
		t.Fatalf("expected one fixed-length patch, got %+v and %d bytes", stats, len(patched))
	}
	for _, fragment := range []string{
		`/^(Bash|Read|Write|Edit)$/.test(t.name)&&state(c).mode==="bypassPermissions"&&!c.forRemoteExecution`,
		`let $x=c,$t=t.name,$i=i;c={...$x,getAppState(){`,
		`/^(user|local|project|flag)Settings$/`,
		`r.split("(")[0]!==$t||n&&n!=="command"&&(Object.hasOwn($i??{},n)||$t==="Bash"&&!r.endsWith(":*)"))`,
		`let y=state(c),d=denials(t,i,c);if(d)return d;`,
		`let b=await t.checkPermissions(i,c);if(b.behavior==="deny")return b;`,
		`if(b.decisionReason?.type==="safetyCheck")return b;`,
	} {
		if !bytes.Contains(patched, []byte(fragment)) {
			t.Errorf("missing fragment %q in patched decision", fragment)
		}
	}
	if !bytes.HasSuffix(patched, []byte("function next(){}")) {
		t.Fatal("patch changed the following function")
	}

	again, stats, err := applyBypassLocalAskPatch(patched, nil, false)
	if err != nil || stats.Changed != 0 || stats.Eligible != 1 || !bytes.Equal(again, patched) {
		t.Fatalf("expected an unchanged patch on reapplication, got %+v, %v", stats, err)
	}
}

func TestBypassLocalAskPatchSkipsIncompatibleLayout(t *testing.T) {
	for _, input := range []string{
		strings.Replace(localAskPatchFixture, "let y=state(c)", "let y=state(i)", 1),
		strings.Replace(localAskPatchFixture, "Permission suggestions for", "Unexpected message for", 1),
		localAskPatchFixture + "/*" + permissionDecisionPatchMarkerV4 + "*/",
		localAskPatchFixture + bashAskPatchV1Marker,
	} {
		patched, stats, err := applyBypassLocalAskPatch([]byte(input), nil, false)
		if err != nil || stats.Eligible != 0 || !bytes.Equal(patched, []byte(input)) {
			t.Errorf("expected unchanged input for an unsupported layout, got %+v, %v", stats, err)
		}
	}
}

func TestLocalAskPatchKeepsNativeHookPath(t *testing.T) {
	specs, err := policySettingsSpecs()
	if err != nil {
		t.Fatal(err)
	}
	hook := observedRuleCheckPermissionDecisionPatchFixture()
	input := append([]byte(localAskPatchFixture), hook...)
	patched, _, err := applyExePatches(input, specs, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(patched, []byte(localAskPatchMarker)) || !bytes.HasSuffix(patched, hook) {
		t.Fatal("expected the local rules patch alongside the original hook path")
	}
}

func TestConfigureLocalAskPatchLaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(localAskPatchMarker), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		outcome patchOutcome
		want    bool
	}{
		{name: "matching patch", outcome: patchOutcome{PatchStats: []exePatchStats{{Label: localAskPatchLabel, Eligible: 1}}}, want: true},
		{name: "no match", outcome: patchOutcome{PatchStats: []exePatchStats{{Label: localAskPatchLabel}}}},
		{name: "in-place history hit", outcome: patchOutcome{AlreadyPatched: true, TargetPath: path}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.outcome.LaunchEnv = []string{"OTHER_ENV=existing"}
			if err := configureLocalAskPatchLaunch(&tc.outcome); err != nil {
				t.Fatal(err)
			}
			if got := slices.Contains(tc.outcome.LaunchEnv, bunSourceLaunchEnv); got != tc.want {
				t.Errorf("source launch environment present = %v, want %v", got, tc.want)
			}
			if !slices.Contains(tc.outcome.LaunchEnv, "OTHER_ENV=existing") {
				t.Error("existing launch environment was discarded")
			}
		})
	}
}
