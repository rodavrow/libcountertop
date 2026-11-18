package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMergeFeaturesLeavesOneDisableFeaturesLast(t *testing.T) {
	got := mergeFeatures([]string{
		"--disable-features=A,B",
		"--kiosk",
		"--enable-features=C",
		"--disable-features=B,D",
	})
	want := []string{"--kiosk", "--enable-features=C", "--disable-features=A,B,D"}
	if !slices.Equal(got, want) {
		t.Errorf("mergeFeatures = %q, want %q", got, want)
	}
}

func TestMergeFeaturesWithoutAnyLeavesTheArgsAlone(t *testing.T) {
	args := []string{"--kiosk", "about:blank"}
	if got := mergeFeatures(args); !slices.Equal(got, args) {
		t.Errorf("mergeFeatures = %q, want %q", got, args)
	}
}

func TestLaunchArgsQuietChromiumAndKeepTheCallersFeatures(t *testing.T) {
	got := launchArgs(9222, []string{"--disable-features=LocalNetworkAccessChecks", "--x"}, true)
	for _, want := range []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=9222",
		"--disable-component-update",
		"--disable-background-networking",
		"--disable-sync",
		"--no-pings",
		"--disable-domain-reliability",
		"--metrics-recording-only",
		"--disable-client-side-phishing-detection",
		"--disable-default-apps",
		"--x",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("launchArgs lacks %s: %q", want, got)
		}
	}
	var features []string
	for _, a := range got {
		if v, ok := strings.CutPrefix(a, "--disable-features="); ok {
			features = append(features, v)
		}
	}
	if len(features) != 1 {
		t.Fatalf("launchArgs has %d --disable-features, want 1: %q", len(features), got)
	}
	for _, want := range []string{
		"LocalNetworkAccessChecks", "Translate", "OptimizationHints", "MediaRouter",
		"DialMediaRouteProvider", "AutofillServerCommunication",
		"CertificateTransparencyComponentUpdater", "DnsOverHttps",
	} {
		if !slices.Contains(strings.Split(features[0], ","), want) {
			t.Errorf("--disable-features lacks %s: %s", want, features[0])
		}
	}
	if got[len(got)-1] != "--disable-features="+features[0] {
		t.Errorf("--disable-features is not last, so a later one would win: %q", got)
	}
}

func TestLaunchArgsNotQuietAreTheUpstreamFlags(t *testing.T) {
	got := launchArgs(9222, []string{"--disable-features=LocalNetworkAccessChecks"}, false)
	for _, q := range quiet {
		if strings.HasPrefix(q, disableFeatures) {
			continue
		}
		if slices.Contains(got, q) {
			t.Errorf("launchArgs(not quiet) has %s", q)
		}
	}
	if last := got[len(got)-1]; last != "--disable-features=LocalNetworkAccessChecks" {
		t.Errorf("launchArgs(not quiet) changed the caller's features: %s", last)
	}
}

func TestWritePolicyNotQuietRemovesAKeptPolicy(t *testing.T) {
	dir := t.TempDir()
	if err := writePolicy(dir, true); err != nil {
		t.Fatal(err)
	}
	if err := writePolicy(dir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, policyPath)); !os.IsNotExist(err) {
		t.Errorf("a policy is left after starting without quiet: %v", err)
	}
	if err := writePolicy(t.TempDir(), false); err != nil {
		t.Errorf("writePolicy(not quiet) with no policy kept = %v", err)
	}
}

func TestRestartWithoutARunningEngineDoesNothing(t *testing.T) {
	NewHost(HostOptions{}).Restart()
}

func TestWritePolicyQuietWritesTheManagedPolicy(t *testing.T) {
	dir := t.TempDir()
	if err := writePolicy(dir, true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "chromium", "policies", "managed", "lanovo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the policy is not JSON: %v", err)
	}
	want := map[string]any{
		"MetricsReportingEnabled":     false,
		"SafeBrowsingProtectionLevel": float64(0),
		"NetworkPredictionOptions":    float64(2),
		"BrowserSignin":               float64(0),
		"SyncDisabled":                true,
		"SpellcheckEnabled":           false,
		"TranslateEnabled":            false,
		"DnsOverHttpsMode":            "off",
		"BackgroundModeEnabled":       false,
		"ComponentUpdatesEnabled":     false,
		"PasswordManagerEnabled":      false,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("policy %s = %v, want %v", k, got[k], v)
		}
	}
}
