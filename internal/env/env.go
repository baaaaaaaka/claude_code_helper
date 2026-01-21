package env

import (
	"strings"
)

var loopbackNoProxy = []string{"localhost", "127.0.0.1", "::1"}

func WithProxy(base []string, proxyURL string) []string {
	m := toMap(base)

	setBoth(m, "HTTP_PROXY", proxyURL)
	setBoth(m, "HTTPS_PROXY", proxyURL)

	noProxy := firstNonEmpty(m["NO_PROXY"], m["no_proxy"])
	noProxy = mergeNoProxy(noProxy, loopbackNoProxy)
	setBoth(m, "NO_PROXY", noProxy)

	return fromMap(m)
}

func mergeNoProxy(existing string, required []string) string {
	set := map[string]bool{}
	var out []string

	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		key := strings.ToLower(v)
		if set[key] {
			return
		}
		set[key] = true
		out = append(out, v)
	}

	for _, part := range strings.Split(existing, ",") {
		add(part)
	}
	for _, req := range required {
		add(req)
	}

	return strings.Join(out, ",")
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func setBoth(m map[string]string, key, value string) {
	m[key] = value
	m[strings.ToLower(key)] = value
}

func toMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		out[k] = v
	}
	return out
}

func fromMap(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
