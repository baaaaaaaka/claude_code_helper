package cli

import (
	"bytes"
	"errors"
	"io"
	"regexp"
)

const maxNonPrintablePercent = 10
const (
	bypassPermissionsGateName        = "tengu_disable_bypass_permissions_mode"
	bypassPermissionsGateNamePatched = "tengu_disable_bypass_permissionX_mode"
	bypassPermissionsSettingKey      = "disableBypassPermissionsMode"
	bypassPermissionsSettingPatched  = "disableBypassPermissionsModE"
	rootBypassGuardCond              = `process.getuid()===0&&process.env.IS_SANDBOX!=="1"&&process.env.CLAUDE_CODE_BUBBLEWRAP!=="1"`
	rootBypassGuardCondPatched       = `process.getuid()===1&&process.env.IS_SANDBOX!=="1"&&process.env.CLAUDE_CODE_BUBBLEWRAP!=="1"`
	rootBypassGuardErrorMessage      = `--dangerously-skip-permissions cannot be used with root/sudo privileges for security reasons`
	rootBypassGuardContextBytes      = 512
	remoteSettingsFileName           = "remote-settings.json"
	remoteSettingsFilePatched        = "remote-settings.jsoN"
	remoteSettingsAPIPath            = "/api/claude_code/settings"
	remoteSettingsAPIPathPatched     = "/api/claude_code/settingS"
)

func applyPolicySettingsDisablePatch(data []byte, startRe *regexp.Regexp, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
	stats := exePatchStats{Label: "policySettings-disable"}
	if startRe == nil {
		return nil, stats, errors.New("policySettings getter regex is nil")
	}

	matches := startRe.FindAllIndex(data, -1)
	if len(matches) == 0 {
		return data, stats, nil
	}
	stats.Segments = len(matches)

	patched := make([]byte, len(data))
	copy(patched, data)

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
		before := patched[contentStart:contentEnd]
		if preview {
			logPatchPreview(log, "policySettings-disable", before, repl)
		}

		stats.Eligible++
		stats.Patched++
		stats.Replacements++
		if !bytes.Equal(before, repl) {
			stats.Changed++
		}
		copy(patched[contentStart:contentEnd], repl)
		lastEnd = blockEnd
	}

	if stats.Eligible == 0 {
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

	changed := false
	patched := make([]byte, len(data))
	copy(patched, data)

	for _, repl := range replacements {
		count := bytes.Count(patched, repl.before)
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
		patched = bytes.ReplaceAll(patched, repl.before, repl.after)
		changed = true
		stats.Changed += count
	}

	if !changed {
		return data, stats, nil
	}
	return patched, stats, nil
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

	changed := false
	patched := make([]byte, len(data))
	copy(patched, data)

	for _, repl := range replacements {
		count := bytes.Count(patched, repl.before)
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
		patched = bytes.ReplaceAll(patched, repl.before, repl.after)
		changed = true
		stats.Changed += count
	}

	if !changed {
		return data, stats, nil
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
