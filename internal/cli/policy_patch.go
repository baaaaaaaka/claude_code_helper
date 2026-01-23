package cli

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

type policyReplacementPlan struct {
	blockStart int
	blockEnd   int
	target     stmtRange
	repl       []byte
	kind       string
}

type stmtRange struct {
	start int
	end   int
}

func applyPolicySettingsBlockPatch(data []byte, startRe *regexp.Regexp, log io.Writer, preview bool) ([]byte, exePatchStats, error) {
	stats := exePatchStats{Label: "policySettings-block"}
	if startRe == nil {
		return nil, stats, errors.New("policySettings start regex is nil")
	}

	matches := startRe.FindAllIndex(data, -1)
	if len(matches) == 0 {
		return nil, stats, fmt.Errorf("stage-1 regex produced no matches")
	}
	stats.Segments = len(matches)

	plans, eligibleBlocks, err := buildPolicySettingsPlans(data, matches)
	if err != nil {
		return nil, stats, err
	}
	stats.Eligible = eligibleBlocks
	if len(plans) == 0 {
		return data, stats, nil
	}

	sort.Slice(plans, func(i, j int) bool {
		return plans[i].target.start < plans[j].target.start
	})
	for i := 1; i < len(plans); i++ {
		if plans[i].target.start < plans[i-1].target.end {
			return nil, stats, fmt.Errorf("overlapping policySettings replacements detected")
		}
	}

	patched := make([]byte, len(data))
	copy(patched, data)

	for _, plan := range plans {
		if plan.target.end <= plan.target.start {
			return nil, stats, fmt.Errorf("empty replacement range")
		}
		if len(plan.repl) != plan.target.end-plan.target.start {
			return nil, stats, fmt.Errorf("replacement length mismatch for %s", plan.kind)
		}

		before := patched[plan.target.start:plan.target.end]
		if preview {
			logPatchPreview(log, "policySettings-"+plan.kind, before, plan.repl)
		}
		if string(before) != string(plan.repl) {
			stats.Changed++
		}
		copy(patched[plan.target.start:plan.target.end], plan.repl)
		stats.Replacements++
	}

	stats.Patched = stats.Replacements
	return patched, stats, nil
}

func buildPolicySettingsPlans(data []byte, matches [][]int) ([]policyReplacementPlan, int, error) {
	seen := make(map[int]struct{})
	plans := make([]policyReplacementPlan, 0, len(matches))
	eligibleBlocks := 0

	for _, match := range matches {
		openBrace := match[1] - 1
		blockStart, blockEnd, ok := findBlock(data, openBrace)
		if !ok {
			continue
		}
		if _, ok := seen[blockStart]; ok {
			continue
		}
		seen[blockStart] = struct{}{}

		plan, ok, err := planPolicyReplacement(data, blockStart, blockEnd)
		if err != nil {
			return nil, 0, err
		}
		if ok {
			eligibleBlocks++
			plans = append(plans, plan)
		}
	}

	return plans, eligibleBlocks, nil
}

func planPolicyReplacement(data []byte, blockStart, blockEnd int) (policyReplacementPlan, bool, error) {
	scan := scanBlock(data, blockStart, blockEnd)
	if scan.firstContinue >= 0 {
		stmt, ok := pickStatementBefore(scan, scan.firstContinue, len("continue;"))
		if ok {
			return policyReplacementPlan{
				blockStart: blockStart,
				blockEnd:   blockEnd,
				target:     stmt,
				repl:       paddedLiteral("continue;", stmt.end-stmt.start),
				kind:       "continue",
			}, true, nil
		}
	}

	if scan.firstReturnNull >= 0 {
		stmt, ok := pickStatementBefore(scan, scan.firstReturnNull, len("return null;"))
		if ok {
			return policyReplacementPlan{
				blockStart: blockStart,
				blockEnd:   blockEnd,
				target:     stmt,
				repl:       paddedLiteral("return null;", stmt.end-stmt.start),
				kind:       "return-null",
			}, true, nil
		}
	}

	return policyReplacementPlan{}, false, nil
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

func pickStatementBefore(scan blockScan, before int, minLen int) (stmtRange, bool) {
	for i := len(scan.statements) - 1; i >= 0; i-- {
		stmt := scan.statements[i]
		if stmt.end > before {
			continue
		}
		if stmt.end-stmt.start < minLen {
			continue
		}
		content := strings.TrimSpace(string(scan.source[stmt.start:stmt.end]))
		if strings.HasPrefix(content, "continue") || strings.HasPrefix(content, "return") {
			continue
		}
		return stmt, true
	}
	return stmtRange{}, false
}

type blockScan struct {
	source          []byte
	statements      []stmtRange
	firstContinue   int
	firstReturnNull int
}

func scanBlock(data []byte, start, end int) blockScan {
	scan := blockScan{
		source:          data,
		firstContinue:   -1,
		firstReturnNull: -1,
	}

	stmtStart := nextNonSpace(data, start+1, end)
	braceDepth := 1
	parenDepth := 0
	inLineComment := false
	inBlockComment := false
	inString := byte(0)
	escaped := false
	lastToken := ""

	for i := start + 1; i < end; i++ {
		ch := data[i]
		next := byte(0)
		if i+1 < end {
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

		switch ch {
		case '{':
			braceDepth++
		case '}':
			braceDepth--
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case ';':
			if braceDepth == 1 && parenDepth == 0 {
				if stmtStart >= 0 && stmtStart < i+1 {
					scan.statements = append(scan.statements, stmtRange{start: stmtStart, end: i + 1})
				}
				stmtStart = nextNonSpace(data, i+1, end)
			}
		default:
		}

		if isJSIdentStart(ch) {
			ident, endIdx := readJSIdent(data, i, end)
			if ident != "" {
				if ident == "continue" && scan.firstContinue < 0 {
					scan.firstContinue = i
				}
				if lastToken == "return" && ident == "null" && scan.firstReturnNull < 0 {
					scan.firstReturnNull = i
				}
				lastToken = ident
				i = endIdx - 1
				continue
			}
		} else if !isJSIdentChar(ch) && !isSpace(ch) {
			lastToken = ""
		}
	}

	return scan
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

func nextNonSpace(data []byte, start, end int) int {
	for i := start; i < end; i++ {
		if !isSpace(data[i]) {
			return i
		}
	}
	return end
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\n' || b == '\r' || b == '\t'
}

func isJSIdentStart(b byte) bool {
	return b == '_' || b == '$' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func isJSIdentChar(b byte) bool {
	return isJSIdentStart(b) || (b >= '0' && b <= '9')
}

func readJSIdent(data []byte, start, end int) (string, int) {
	if start >= end || !isJSIdentStart(data[start]) {
		return "", start
	}
	i := start + 1
	for i < end && isJSIdentChar(data[i]) {
		i++
	}
	return string(data[start:i]), i
}
