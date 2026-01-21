package env

import (
	"strings"
	"testing"
)

func TestWithProxy_SetsProxyAndMergesNoProxy(t *testing.T) {
	base := []string{
		"PATH=/bin",
		"NO_PROXY=example.com,localhost",
	}

	out := WithProxy(base, "http://127.0.0.1:8080")
	m := toMap(out)

	if got := m["HTTP_PROXY"]; got != "http://127.0.0.1:8080" {
		t.Fatalf("HTTP_PROXY=%q", got)
	}
	if got := m["http_proxy"]; got != "http://127.0.0.1:8080" {
		t.Fatalf("http_proxy=%q", got)
	}
	if got := m["HTTPS_PROXY"]; got != "http://127.0.0.1:8080" {
		t.Fatalf("HTTPS_PROXY=%q", got)
	}
	if got := m["https_proxy"]; got != "http://127.0.0.1:8080" {
		t.Fatalf("https_proxy=%q", got)
	}

	noProxy := firstNonEmpty(m["NO_PROXY"], m["no_proxy"])
	for _, want := range []string{"example.com", "localhost", "127.0.0.1", "::1"} {
		if !containsCSV(noProxy, want) {
			t.Fatalf("NO_PROXY=%q missing %q", noProxy, want)
		}
	}
}

func TestWithProxy_PreservesExistingLowercaseNoProxy(t *testing.T) {
	base := []string{
		"no_proxy=foo.local",
	}

	out := WithProxy(base, "http://127.0.0.1:8080")
	m := toMap(out)

	noProxy := firstNonEmpty(m["NO_PROXY"], m["no_proxy"])
	if !containsCSV(noProxy, "foo.local") {
		t.Fatalf("NO_PROXY=%q missing foo.local", noProxy)
	}
}

func containsCSV(csv, needle string) bool {
	for _, part := range strings.Split(csv, ",") {
		if strings.EqualFold(strings.TrimSpace(part), needle) {
			return true
		}
	}
	return false
}
