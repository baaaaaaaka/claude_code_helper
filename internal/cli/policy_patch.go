package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const maxNonPrintablePercent = 10
const (
	bypassPermissionsGateName        = "tengu_disable_bypass_permissions_mode"
	bypassPermissionsGateNamePatched = "tengu_disable_bypass_permissionX_mode"
	bypassPermissionsSettingKey      = "disableBypassPermissionsMode"
	bypassPermissionsSettingPatched  = "disableBypassPermissionsModE"
	rootBypassGuardCond              = `process.getuid()===0&&process.env.IS_SANDBOX!=="1"`
	rootBypassGuardCondPatched       = `process.getuid()===1&&process.env.IS_SANDBOX!=="1"`
	rootBypassGuardErrorMessage      = `--dangerously-skip-permissions cannot be used with root/sudo privileges for security reasons`
	rootBypassGuardContextBytes      = 512
	remoteSettingsFileName           = "remote-settings.json"
	remoteSettingsFilePatched        = "remote-settings.jsoN"
	remoteSettingsAPIPath            = "/api/claude_code/settings"
	remoteSettingsAPIPathPatched     = "/api/claude_code/settingS"
	permissionDecisionAskRuleAnchor  = "ask rule/safety check requires full permission pipeline"
	permissionDecisionPatchMarker    = "tengu_bypass_permission_decision_v1"
)

var policySettingsDirectReturnRe = regexp.MustCompile(policySettingsDirectReturnStage1)
var permissionDecisionFunctionRe = regexp.MustCompile(`async function\s+([A-Za-z0-9_$]+)\s*\(([^)]*)\)\s*\{`)
var permissionDecisionRuleCheckRe = regexp.MustCompile(`let\s+[A-Za-z0-9_$]+\s*=\s*await\s+([A-Za-z0-9_$]+)\s*\(`)

func applyPolicySettingsDisablePatch(data []byte, startRe *regexp.Regexp, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
	stats := exePatchStats{Label: "policySettings-disable"}
	if startRe == nil {
		return nil, stats, errors.New("policySettings getter regex is nil")
	}

	out, blockStats, err := applyPolicySettingsBlockDisablePatch(data, startRe, log, preview)
	if err != nil {
		return nil, stats, err
	}
	stats.add(blockStats)

	out, returnStats, err := applyPolicySettingsDirectReturnDisablePatch(out, policySettingsDirectReturnRe, log, preview)
	if err != nil {
		return nil, stats, err
	}
	stats.add(returnStats)

	return out, stats, nil
}

func (s *exePatchStats) add(other exePatchStats) {
	s.Segments += other.Segments
	s.Eligible += other.Eligible
	s.Patched += other.Patched
	s.Changed += other.Changed
	s.Replacements += other.Replacements
}

func applyPolicySettingsBlockDisablePatch(data []byte, startRe *regexp.Regexp, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
	stats := exePatchStats{Label: "policySettings-disable"}
	matches := startRe.FindAllIndex(data, -1)
	if len(matches) == 0 {
		return data, stats, nil
	}
	stats.Segments = len(matches)

	var patched []byte

	lastEnd := 0
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		ifOpen := match[1] - 1
		if ifOpen < 0 || ifOpen >= len(data) || data[ifOpen] != '{' {
			continue
		}

		blockStart, blockEnd, ok := findBlock(data, ifOpen)
		if !ok {
			continue
		}
		if blockStart < lastEnd {
			continue
		}
		if !looksLikePolicyTextBlock(data[blockStart:blockEnd]) {
			continue
		}

		contentStart := blockStart + 1
		contentEnd := blockEnd - 1
		if contentEnd <= contentStart {
			continue
		}

		repl := paddedLiteral("return null;", contentEnd-contentStart)
		before := data[contentStart:contentEnd]
		if preview {
			logPatchPreview(log, "policySettings-disable", before, repl)
		}

		stats.Eligible++
		stats.Patched++
		stats.Replacements++
		if !bytes.Equal(before, repl) {
			stats.Changed++
			if patched == nil {
				patched = make([]byte, len(data))
				copy(patched, data)
			}
			copy(patched[contentStart:contentEnd], repl)
		}
		lastEnd = blockEnd
	}

	if stats.Eligible == 0 {
		return data, stats, nil
	}
	if patched == nil {
		return data, stats, nil
	}
	return patched, stats, nil
}

func applyPolicySettingsDirectReturnDisablePatch(data []byte, startRe *regexp.Regexp, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
	stats := exePatchStats{Label: "policySettings-disable"}
	if startRe == nil {
		return nil, stats, errors.New("policySettings direct-return regex is nil")
	}

	matches := startRe.FindAllIndex(data, -1)
	if len(matches) == 0 {
		return data, stats, nil
	}
	stats.Segments = len(matches)

	var patched []byte
	lastEnd := 0
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		if match[0] < lastEnd {
			continue
		}
		segment := data[match[0]:match[1]]
		returnRel := bytes.Index(segment, []byte("return"))
		semiRel := bytes.LastIndexByte(segment, ';')
		if returnRel < 0 || semiRel <= returnRel {
			continue
		}

		contentStart := match[0] + returnRel
		contentEnd := match[0] + semiRel
		if contentEnd <= contentStart {
			continue
		}

		repl := paddedLiteral("return null", contentEnd-contentStart)
		before := data[contentStart:contentEnd]
		if preview {
			logPatchPreview(log, "policySettings-disable", before, repl)
		}

		stats.Eligible++
		stats.Patched++
		stats.Replacements++
		if !bytes.Equal(before, repl) {
			stats.Changed++
			if patched == nil {
				patched = make([]byte, len(data))
				copy(patched, data)
			}
			copy(patched[contentStart:contentEnd], repl)
		}
		lastEnd = match[1]
	}

	if stats.Eligible == 0 {
		return data, stats, nil
	}
	if patched == nil {
		return data, stats, nil
	}
	return patched, stats, nil
}

func applyBypassPermissionsGatePatch(data []byte, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
	stats := exePatchStats{Label: "bypass-permissions-gate"}
	replacements := []struct {
		before []byte
		after  []byte
		label  string
	}{
		{[]byte(bypassPermissionsGateName), []byte(bypassPermissionsGateNamePatched), "statsig-gate"},
		{[]byte(bypassPermissionsSettingKey), []byte(bypassPermissionsSettingPatched), "settings-key"},
	}

	total := 0
	for _, repl := range replacements {
		total += bytes.Count(data, repl.before)
	}
	if total == 0 {
		return data, stats, nil
	}

	patched := make([]byte, len(data))
	copy(patched, data)

	for _, repl := range replacements {
		count := bytes.Count(data, repl.before)
		if count == 0 {
			continue
		}
		if preview {
			logPatchPreview(log, stats.Label+"-"+repl.label, repl.before, repl.after)
		}
		stats.Segments += count
		stats.Eligible += count
		stats.Patched += count
		stats.Replacements += count
		stats.Changed += count
		replaceAllFixedLengthInPlace(patched, repl.before, repl.after)
	}

	return patched, stats, nil
}

func applyBypassPermissionDecisionPatch(data []byte, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
	stats := exePatchStats{Label: "bypass-permission-decision"}
	anchor := []byte(permissionDecisionAskRuleAnchor)
	marker := []byte(permissionDecisionPatchMarker)
	markerCount := bytes.Count(data, marker)

	if bytes.Count(data, anchor) == 0 {
		if markerCount == 0 {
			return data, stats, nil
		}
		markBypassPermissionDecisionAlreadyPatched(&stats, markerCount)
		return data, stats, nil
	}

	var patched []byte
	lastEnd := 0
	searchStart := 0
	for {
		rel := bytes.Index(data[searchStart:], anchor)
		if rel < 0 {
			break
		}
		anchorIdx := searchStart + rel
		searchStart = anchorIdx + len(anchor)
		stats.Segments++

		start, end, ok := findPermissionDecisionFunction(data, anchorIdx)
		if !ok || start < lastEnd {
			continue
		}
		segment := data[start:end]
		if !looksLikePermissionDecisionFunction(segment) {
			continue
		}
		replacement, err := buildBypassPermissionDecisionReplacement(segment)
		if err != nil {
			return nil, stats, err
		}
		if len(replacement) > len(segment) {
			return nil, stats, fmt.Errorf("bypass permission decision replacement is longer than target segment: %d > %d", len(replacement), len(segment))
		}

		repl := paddedLiteral(replacement, len(segment))
		if preview {
			logPatchPreview(log, stats.Label, segment, repl)
		}
		stats.Eligible++
		stats.Patched++
		stats.Replacements++
		if !bytes.Equal(segment, repl) {
			stats.Changed++
			if patched == nil {
				patched = make([]byte, len(data))
				copy(patched, data)
			}
			copy(patched[start:end], repl)
		}
		lastEnd = end
	}

	if stats.Eligible == 0 {
		if markerCount > 0 {
			markBypassPermissionDecisionAlreadyPatched(&stats, markerCount)
		}
		return data, stats, nil
	}
	if patched == nil {
		return data, stats, nil
	}
	return patched, stats, nil
}

func markBypassPermissionDecisionAlreadyPatched(stats *exePatchStats, markerCount int) {
	stats.Segments += markerCount
	stats.Eligible += markerCount
	stats.Patched += markerCount
	stats.Replacements += markerCount
}

func applyRemoteSettingsDisablePatch(data []byte, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
	stats := exePatchStats{Label: "remote-settings-disable"}
	replacements := []struct {
		before []byte
		after  []byte
		label  string
	}{
		{[]byte(remoteSettingsFileName), []byte(remoteSettingsFilePatched), "file-name"},
		{[]byte(remoteSettingsAPIPath), []byte(remoteSettingsAPIPathPatched), "api-path"},
	}

	total := 0
	for _, repl := range replacements {
		total += bytes.Count(data, repl.before)
	}
	if total == 0 {
		return data, stats, nil
	}

	patched := make([]byte, len(data))
	copy(patched, data)

	for _, repl := range replacements {
		count := bytes.Count(data, repl.before)
		if count == 0 {
			continue
		}
		if preview {
			logPatchPreview(log, stats.Label+"-"+repl.label, repl.before, repl.after)
		}
		stats.Segments += count
		stats.Eligible += count
		stats.Patched += count
		stats.Replacements += count
		stats.Changed += count
		replaceAllFixedLengthInPlace(patched, repl.before, repl.after)
	}

	return patched, stats, nil
}

func applyRootBypassGuardPatch(data []byte, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
	stats := exePatchStats{Label: "root-bypass-guard"}
	before := []byte(rootBypassGuardCond)
	after := []byte(rootBypassGuardCondPatched)
	msg := []byte(rootBypassGuardErrorMessage)

	totalMatches := bytes.Count(data, before)
	if totalMatches == 0 {
		return data, stats, nil
	}
	stats.Segments = totalMatches

	allIndices := make([]int, 0, totalMatches)
	searchStart := 0
	for {
		rel := bytes.Index(data[searchStart:], before)
		if rel < 0 {
			break
		}
		idx := searchStart + rel
		allIndices = append(allIndices, idx)
		searchStart = idx + 1
	}

	indices := make([]int, 0, len(allIndices))
	for i, idx := range allIndices {
		segmentEnd := len(data)
		if i+1 < len(allIndices) {
			segmentEnd = allIndices[i+1]
		}
		windowStart := idx + len(before)
		if windowStart >= segmentEnd {
			continue
		}
		windowEnd := idx + len(before) + rootBypassGuardContextBytes
		if windowEnd > segmentEnd {
			windowEnd = segmentEnd
		}
		if windowEnd > len(data) {
			windowEnd = len(data)
		}
		if windowEnd <= windowStart {
			continue
		}
		if !bytes.Contains(data[windowStart:windowEnd], msg) {
			continue
		}
		indices = append(indices, idx)
	}

	if len(indices) == 0 {
		return data, stats, nil
	}
	if preview {
		logPatchPreview(log, stats.Label, before, after)
	}

	patched := make([]byte, len(data))
	copy(patched, data)
	for _, idx := range indices {
		copy(patched[idx:idx+len(before)], after)
	}
	stats.Eligible = len(indices)
	stats.Patched = len(indices)
	stats.Replacements = len(indices)
	stats.Changed = len(indices)
	return patched, stats, nil
}

func replaceAllFixedLengthInPlace(data []byte, before []byte, after []byte) {
	if len(before) == 0 || len(before) != len(after) {
		return
	}
	searchStart := 0
	for {
		rel := bytes.Index(data[searchStart:], before)
		if rel < 0 {
			return
		}
		idx := searchStart + rel
		copy(data[idx:idx+len(after)], after)
		searchStart = idx + len(after)
	}
}

func paddedLiteral(literal string, length int) []byte {
	if length <= len(literal) {
		return []byte(literal[:length])
	}
	out := make([]byte, length)
	copy(out, literal)
	for i := len(literal); i < length; i++ {
		out[i] = ' '
	}
	return out
}

func findPermissionDecisionFunction(data []byte, anchorIdx int) (int, int, bool) {
	if anchorIdx < 0 || anchorIdx > len(data) {
		return 0, 0, false
	}
	windowStart := anchorIdx - 4096
	if windowStart < 0 {
		windowStart = 0
	}
	sigRel := bytes.LastIndex(data[windowStart:anchorIdx], []byte("async function "))
	if sigRel < 0 {
		return 0, 0, false
	}
	start := windowStart + sigRel
	openRel := bytes.IndexByte(data[start:anchorIdx], '{')
	if openRel < 0 {
		return 0, 0, false
	}
	blockStart := start + openRel
	_, blockEnd, ok := findBlock(data, blockStart)
	if !ok || blockEnd <= anchorIdx {
		return 0, 0, false
	}
	return start, blockEnd, true
}

func looksLikePermissionDecisionFunction(segment []byte) bool {
	return bytes.Contains(segment, []byte(".requiresUserInteraction?.()")) &&
		bytes.Contains(segment, []byte("canUseTool is required")) &&
		bytes.Contains(segment, []byte(permissionDecisionAskRuleAnchor))
}

func buildBypassPermissionDecisionReplacement(segment []byte) (string, error) {
	headerEnd := bytes.IndexByte(segment, '{')
	if headerEnd < 0 {
		return "", errors.New("permission decision function missing opening brace")
	}
	match := permissionDecisionFunctionRe.FindSubmatch(segment[:headerEnd+1])
	if len(match) != 3 {
		return "", errors.New("permission decision function signature did not match")
	}
	name := string(match[1])
	params := splitJSParams(string(match[2]))
	if len(params) != 7 {
		return "", fmt.Errorf("permission decision function has %d parameters, expected 7", len(params))
	}
	ruleCheck := findPermissionDecisionRuleCheckName(segment)
	if ruleCheck == "" {
		return "", errors.New("permission decision rule-check function did not match")
	}
	locals, err := pickJSLocalNames(params, 5)
	if err != nil {
		return "", err
	}

	hookResult := params[0]
	tool := params[1]
	input := params[2]
	context := params[3]
	decide := params[4]
	assistantMessage := params[5]
	toolUseID := params[6]
	effectiveInput := locals[0]
	behavior := locals[1]
	requiresInteraction := locals[2]
	hasUpdatedInput := locals[3]
	ruleDecision := locals[4]

	var b strings.Builder
	b.WriteString("async function ")
	b.WriteString(name)
	b.WriteByte('(')
	b.WriteString(strings.Join(params, ","))
	b.WriteString("){if(")
	b.WriteString(hookResult)
	b.WriteString("?.behavior===\"deny\")return{decision:")
	b.WriteString(hookResult)
	b.WriteString(",input:")
	b.WriteString(input)
	b.WriteString("};let ")
	b.WriteString(effectiveInput)
	b.WriteByte('=')
	b.WriteString(hookResult)
	b.WriteString("?.updatedInput??")
	b.WriteString(input)
	b.WriteString(";if(")
	b.WriteString(context)
	b.WriteString(".getAppState().toolPermissionContext.mode===\"bypassPermissions\")return{decision:{behavior:\"allow\",updatedInput:")
	b.WriteString(effectiveInput)
	b.WriteString(",decisionReason:{type:\"mode\",mode:\"bypassPermissions\"}},input:")
	b.WriteString(effectiveInput)
	b.WriteString("};if(")
	b.WriteString(hookResult)
	b.WriteString("?.behavior!==\"allow\"&&")
	b.WriteString(hookResult)
	b.WriteString("?.behavior!==\"ask\")return{decision:await ")
	b.WriteString(decide)
	b.WriteByte('(')
	b.WriteString(tool)
	b.WriteByte(',')
	b.WriteString(input)
	b.WriteByte(',')
	b.WriteString(context)
	b.WriteByte(',')
	b.WriteString(assistantMessage)
	b.WriteByte(',')
	b.WriteString(toolUseID)
	b.WriteString("),input:")
	b.WriteString(input)
	b.WriteString("};let ")
	b.WriteString(behavior)
	b.WriteByte('=')
	b.WriteString(hookResult)
	b.WriteString(".behavior,")
	b.WriteString(requiresInteraction)
	b.WriteByte('=')
	b.WriteString(tool)
	b.WriteString(".requiresUserInteraction?.(),")
	b.WriteString(hasUpdatedInput)
	b.WriteByte('=')
	b.WriteString(requiresInteraction)
	b.WriteString("&&")
	b.WriteString(hookResult)
	b.WriteString(".updatedInput!==void 0;if(")
	b.WriteString(behavior)
	b.WriteString("===\"allow\"&&(")
	b.WriteString(requiresInteraction)
	b.WriteString("&&!")
	b.WriteString(hasUpdatedInput)
	b.WriteString("||")
	b.WriteString(context)
	b.WriteString(".requireCanUseTool))return{decision:await ")
	b.WriteString(decide)
	b.WriteByte('(')
	b.WriteString(tool)
	b.WriteByte(',')
	b.WriteString(effectiveInput)
	b.WriteByte(',')
	b.WriteString(context)
	b.WriteByte(',')
	b.WriteString(assistantMessage)
	b.WriteByte(',')
	b.WriteString(toolUseID)
	b.WriteString("),input:")
	b.WriteString(effectiveInput)
	b.WriteString("};let ")
	b.WriteString(ruleDecision)
	b.WriteString("=await ")
	b.WriteString(ruleCheck)
	b.WriteByte('(')
	b.WriteString(tool)
	b.WriteByte(',')
	b.WriteString(effectiveInput)
	b.WriteByte(',')
	b.WriteString(context)
	b.WriteString(");if(")
	b.WriteString(ruleDecision)
	b.WriteString("?.behavior===\"deny\")return{decision:")
	b.WriteString(ruleDecision)
	b.WriteString(",input:")
	b.WriteString(effectiveInput)
	b.WriteString("};if(")
	b.WriteString(ruleDecision)
	b.WriteString("?.behavior===\"ask\")return{decision:await ")
	b.WriteString(decide)
	b.WriteByte('(')
	b.WriteString(tool)
	b.WriteByte(',')
	b.WriteString(effectiveInput)
	b.WriteByte(',')
	b.WriteString(context)
	b.WriteByte(',')
	b.WriteString(assistantMessage)
	b.WriteByte(',')
	b.WriteString(toolUseID)
	b.WriteString("),input:")
	b.WriteString(effectiveInput)
	b.WriteString("};if(")
	b.WriteString(behavior)
	b.WriteString("===\"allow\")return{decision:")
	b.WriteString(hookResult)
	b.WriteString(",input:")
	b.WriteString(effectiveInput)
	b.WriteString("};return{decision:await ")
	b.WriteString(decide)
	b.WriteByte('(')
	b.WriteString(tool)
	b.WriteByte(',')
	b.WriteString(effectiveInput)
	b.WriteByte(',')
	b.WriteString(context)
	b.WriteByte(',')
	b.WriteString(assistantMessage)
	b.WriteByte(',')
	b.WriteString(toolUseID)
	b.WriteByte(',')
	b.WriteString(hookResult)
	b.WriteString("),input:")
	b.WriteString(effectiveInput)
	b.WriteString("}}/*")
	b.WriteString(permissionDecisionPatchMarker)
	b.WriteString("*/")
	return b.String(), nil
}

func splitJSParams(raw string) []string {
	parts := strings.Split(raw, ",")
	params := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		params = append(params, part)
	}
	return params
}

func findPermissionDecisionRuleCheckName(segment []byte) string {
	search := segment
	if anchorIdx := bytes.Index(segment, []byte(permissionDecisionAskRuleAnchor)); anchorIdx >= 0 {
		search = segment[:anchorIdx]
	}
	matches := permissionDecisionRuleCheckRe.FindAllSubmatch(search, -1)
	if len(matches) == 0 {
		return ""
	}
	match := matches[len(matches)-1]
	if len(match) != 2 {
		return ""
	}
	return string(match[1])
}

func pickJSLocalNames(params []string, count int) ([]string, error) {
	used := make(map[string]bool, len(params)+count)
	for _, param := range params {
		used[param] = true
	}
	candidates := []string{"O", "M", "w", "D", "j", "Y", "f", "L", "P", "Q", "B", "I", "u", "F", "g", "l", "c", "i", "e", "s"}
	out := make([]string, 0, count)
	for _, candidate := range candidates {
		if used[candidate] {
			continue
		}
		used[candidate] = true
		out = append(out, candidate)
		if len(out) == count {
			return out, nil
		}
	}
	return nil, fmt.Errorf("not enough local identifiers for permission decision patch: need %d, got %d", count, len(out))
}

func findBlock(data []byte, openBrace int) (int, int, bool) {
	if openBrace < 0 || openBrace >= len(data) || data[openBrace] != '{' {
		return 0, 0, false
	}

	braceDepth := 1
	inLineComment := false
	inBlockComment := false
	inString := byte(0)
	escaped := false

	for i := openBrace + 1; i < len(data); i++ {
		ch := data[i]
		next := byte(0)
		if i+1 < len(data) {
			next = data[i+1]
		}

		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inString != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == inString {
				inString = 0
			}
			continue
		}

		if ch == '/' && next == '/' {
			inLineComment = true
			i++
			continue
		}
		if ch == '/' && next == '*' {
			inBlockComment = true
			i++
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			inString = ch
			continue
		}

		if ch == '{' {
			braceDepth++
		} else if ch == '}' {
			braceDepth--
			if braceDepth == 0 {
				return openBrace, i + 1, true
			}
		}
	}

	return 0, 0, false
}

func looksLikePolicyTextBlock(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	maxNonPrintable := len(data) * maxNonPrintablePercent / 100
	nonPrintable := 0
	for _, b := range data {
		if b == 0 {
			return false
		}
		if isPolicyPrintable(b) {
			continue
		}
		nonPrintable++
		if nonPrintable > maxNonPrintable {
			return false
		}
	}
	return true
}

func isPolicyPrintable(b byte) bool {
	if b == '\n' || b == '\r' || b == '\t' {
		return true
	}
	return b >= 0x20 && b <= 0x7E
}
