package webreceiver

import (
	"slices"
	"strings"
	"testing"
)

func TestLocalEngineArgsDropTheWebSecurityExceptions(t *testing.T) {
	got := LocalEngineArgs()
	for _, a := range got {
		if a == "--allow-running-insecure-content" || strings.HasPrefix(a, "--disable-features=") {
			t.Errorf("LocalEngineArgs keeps %s", a)
		}
	}
	for _, want := range []string{"--autoplay-policy=no-user-gesture-required"} {
		if !slices.Contains(got, want) {
			t.Errorf("LocalEngineArgs lacks %s", want)
		}
	}
	if !slices.ContainsFunc(got, func(a string) bool { return strings.Contains(a, "CrKey/") }) {
		t.Error("LocalEngineArgs lost the Cast user agent")
	}
}

func TestEngineArgsKeepTheWebSecurityExceptions(t *testing.T) {
	if !slices.Contains(EngineArgs(), "--allow-running-insecure-content") {
		t.Error("EngineArgs lost the exceptions Google-hosted receiver pages need")
	}
}
