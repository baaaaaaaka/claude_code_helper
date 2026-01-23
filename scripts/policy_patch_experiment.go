package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	policyStartPattern = `if\([A-Za-z0-9_$-]+===['"]policySettings['"]\)\{`
)

type replacementPlan struct {
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

func main() {
	var (
		inputPath  string
		outputPath string
		preview    bool
		limit      int
	)

	flag.StringVar(&inputPath, "input", "", "Input binary path (default: resolved claude)")
	flag.StringVar(&outputPath, "out", "", "Output path to write patched copy (optional)")
	flag.BoolVar(&preview, "preview", true, "Print before/after replacements")
	flag.IntVar(&limit, "preview-limit", 220, "Max bytes to show per preview")
	flag.Parse()

	path, err := resolveInputPath(inputPath)
	if err != nil {
		exitErr(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		exitErr(fmt.Errorf("read input: %w", err))
	}

	plans, err := buildPlans(data)
	if err != nil {
		exitErr(err)
	}
	if len(plans) == 0 {
		exitErr(errors.New("no policySettings blocks matched"))
	}

	patched, err := applyPlans(data, plans, preview, limit, os.Stdout)
	if err != nil {
		exitErr(err)
	}

	if outputPath != "" {
		if err := os.WriteFile(outputPath, patched, 0o700); err != nil {
			exitErr(fmt.Errorf("write output: %w", err))
		}
	}

	fmt.Printf("matched blocks: %d, replacements: %d\n", countBlocks(plans), len(plans))
}

func resolveInputPath(input string) (string, error) {
	if input != "" {
		return filepath.Abs(input)
	}
	exe, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("find claude: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve claude: %w", err)
	}
	return filepath.Abs(resolved)
}

func buildPlans(data []byte) ([]replacementPlan, error) {
	startRe := regexp.MustCompile(policyStartPattern)
	matches := startRe.FindAllIndex(data, -1)
	if len(matches) == 0 {
		return nil, nil
	}

	var plans []replacementPlan
	seen := make(map[int]struct{})
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

		plan, ok, err := planReplacement(data, blockStart, blockEnd)
		if err != nil {
			return nil, err
		}
		if ok {
			plans = append(plans, plan)
		}
	}

	return plans, nil
}

func planReplacement(data []byte, blockStart, blockEnd int) (replacementPlan, bool, error) {
	scan := scanBlock(data, blockStart, blockEnd)
	if scan.firstContinue >= 0 {
		stmt, ok := pickStatementBefore(scan, scan.firstContinue, len("continue;"))
		if ok {
			return replacementPlan{
				blockStart: blockStart,
				blockEnd:   blockEnd,
				target:     stmt,
				repl:       paddedReplacement("continue;", stmt.end-stmt.start),
				kind:       "continue",
			}, true, nil
		}
	}

	if scan.firstReturnNull >= 0 {
		stmt, ok := pickStatementBefore(scan, scan.firstReturnNull, len("return null;"))
		if ok {
			return replacementPlan{
				blockStart: blockStart,
				blockEnd:   blockEnd,
				target:     stmt,
				repl:       paddedReplacement("return null;", stmt.end-stmt.start),
				kind:       "return-null",
			}, true, nil
		}
	}

	return replacementPlan{}, false, nil
}

func applyPlans(data []byte, plans []replacementPlan, preview bool, limit int, out io.Writer) ([]byte, error) {
	patched := make([]byte, len(data))
	copy(patched, data)

	for _, plan := range plans {
		if plan.target.end <= plan.target.start {
			return nil, fmt.Errorf("empty replacement range")
		}
		if len(plan.repl) != plan.target.end-plan.target.start {
			return nil, fmt.Errorf("replacement length mismatch for %s", plan.kind)
		}

		if preview {
			before := patched[plan.target.start:plan.target.end]
			after := plan.repl
			fmt.Fprintf(out, "[%s] before=%s\n", plan.kind, previewBytes(before, limit))
			fmt.Fprintf(out, "[%s] after=%s\n", plan.kind, previewBytes(after, limit))
		}

		copy(patched[plan.target.start:plan.target.end], plan.repl)
	}

	return patched, nil
}

func countBlocks(plans []replacementPlan) int {
	seen := make(map[int]struct{})
	for _, plan := range plans {
		seen[plan.blockStart] = struct{}{}
	}
	return len(seen)
}

func previewBytes(b []byte, limit int) string {
	if limit <= 0 || len(b) <= limit {
		return fmt.Sprintf("%q", b)
	}
	return fmt.Sprintf("%q...(truncated %d bytes)", b[:limit], len(b)-limit)
}

func paddedReplacement(repl string, length int) []byte {
	if length <= len(repl) {
		return []byte(repl[:length])
	}
	out := make([]byte, length)
	copy(out, repl)
	for i := len(repl); i < length; i++ {
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

		if isIdentStart(ch) {
			ident, endIdx := readIdent(data, i, end)
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
		} else if !isIdentChar(ch) && !isSpace(ch) {
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

func isIdentStart(b byte) bool {
	return b == '_' || b == '$' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func isIdentChar(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}

func readIdent(data []byte, start, end int) (string, int) {
	if start >= end || !isIdentStart(data[start]) {
		return "", start
	}
	i := start + 1
	for i < end && isIdentChar(data[i]) {
		i++
	}
	return string(data[start:i]), i
}

func exitErr(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
