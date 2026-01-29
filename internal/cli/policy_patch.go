package cli

import (
	"bytes"
	"errors"
	"io"
	"regexp"
)

const maxNonPrintablePercent = 10

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
