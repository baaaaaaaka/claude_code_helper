package proc

import "strings"

func LooksLikeProxyDaemon(args []string) bool {
	args = trimProcessArgs(args)
	if len(args) == 0 {
		return false
	}
	if hasOrderedTokens(args, "proxy", "daemon") {
		return true
	}
	return hasOrderedTokens(args, "proxy", "start") && hasBoolFlag(args, "--foreground")
}

func trimProcessArgs(args []string) []string {
	if len(args) <= 1 {
		return nil
	}
	args = args[1:]
	for i, arg := range args {
		if arg == "--" {
			return args[:i]
		}
	}
	return args
}

func hasOrderedTokens(args []string, want ...string) bool {
	if len(want) == 0 {
		return true
	}
	next := 0
	for _, arg := range args {
		if arg != want[next] {
			continue
		}
		next++
		if next == len(want) {
			return true
		}
	}
	return false
}

func hasBoolFlag(args []string, want string) bool {
	for _, arg := range args {
		if arg == want || strings.HasPrefix(arg, want+"=") {
			return true
		}
	}
	return false
}
