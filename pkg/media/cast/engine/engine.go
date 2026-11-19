package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ygelfand/libcountertop/pkg/network/releases"
	"github.com/ygelfand/libcountertop/pkg/runtime/payload"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
)

const (
	repo       = "https://github.com/ygelfand/libcountertop"
	Tag        = "engine-full-1"
	asset      = "engine-full-armhf.tar.xz"
	sum        = "ee70db08ab23ffa6b0b05e33dfdac769f3f729e8d213c2c3ee3e3461257d455a"
	size       = 102977724
	maxArchive = 256 << 20
	firstToGo  = 1000
	diskCache  = 16 << 20
	scratch    = "size=512m,mode=0700"
)

type Options struct {
	Root        string
	State       string
	Port        int
	Display     string
	Args        []string
	Nameservers func() []string
	Quiet       bool
}

func Install(ctx context.Context, dir string, progress func(at float32)) (string, error) {
	root := filepath.Join(dir, Tag)
	if _, err := os.Stat(root); err == nil {
		return root, nil
	}
	if err := wipe(dir, Tag); err != nil {
		return "", err
	}
	partial := root + ".partial"
	report := func(done int64) {
		if progress != nil {
			progress(min(float32(done)/size, 1))
		}
	}
	err := releases.Stream(
		ctx,
		releases.Asset(repo, Tag, asset),
		sum,
		maxArchive,
		report,
		func(r io.Reader) error { return unpack(r, partial) },
	)
	if err != nil {
		return "", errors.Join(err, os.RemoveAll(partial))
	}
	if err := os.Rename(partial, root); err != nil {
		return "", fmt.Errorf("engine: %w", err)
	}
	return root, nil
}

func Ready(ctx context.Context, port int) error {
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/json/version"
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if up(ctx, url) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func session(ctx context.Context, o Options) error {
	etc, err := writeEtc(filepath.Join(o.State, "etc"), o.Nameservers())
	if err != nil {
		return err
	}
	if err := writePolicy(etc, o.Quiet); err != nil {
		return err
	}
	target, err := filepath.EvalSymlinks("/etc")
	if err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	tidy(filepath.Join(o.State, "profile"))
	tmp := filepath.Join(o.State, "tmp")
	if err := wipe(tmp, ""); err != nil {
		return err
	}
	p, err := payload.Start(payload.Command{
		Path: filepath.Join(o.Root, "chromium.sh"),
		Args: append(launchArgs(o.Port, o.Args, o.Quiet), "about:blank"),
		Env: []string{
			"CAST_ENGINE_STATE=" + o.State,
			"WAYLAND_DISPLAY=" + o.Display,
			"XDG_RUNTIME_DIR=" + filepath.Dir(o.Display),
		},
		Log: "cast engine",
		Mounts: []payload.Mount{
			{Source: etc, Target: target},
			{Source: "tmpfs", Target: tmp, Type: "tmpfs", Data: scratch},
		},
		OOMScore: firstToGo,
	})
	if err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	stop := context.AfterFunc(ctx, p.Stop)
	defer stop()
	if err := p.Wait(); err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	return errors.New("engine: chromium exited")
}

// quiet turns off what Chromium fetches on its own: field trials, Safe Browsing lists, push,
// spell-check dictionaries, translation, optimization hints, Cast discovery and DNS-over-HTTPS
// probes. An engine started quiet still shows every page it is told to.
var quiet = []string{
	"--disable-background-networking",
	"--disable-sync",
	"--no-pings",
	"--disable-domain-reliability",
	"--metrics-recording-only",
	"--disable-client-side-phishing-detection",
	"--disable-default-apps",
	"--disable-component-extensions-with-background-pages",
	"--disable-features=Translate,OptimizationHints,MediaRouter,DialMediaRouteProvider," +
		"AutofillServerCommunication,CertificateTransparencyComponentUpdater,DnsOverHttps",
}

func launchArgs(port int, extra []string, quieted bool) []string {
	args := []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(port),
		"--ozone-platform=wayland",
		"--kiosk",
		"--no-first-run",
		"--disable-component-update",
		"--disk-cache-size=" + strconv.Itoa(diskCache),
	}
	if quieted {
		args = append(args, quiet...)
	}
	return mergeFeatures(slices.Concat(args, extra))
}

const disableFeatures = "--disable-features="

// mergeFeatures folds every --disable-features into one, last: Chromium reads only the last one
// it is given, so a caller's list would otherwise silently undo the engine's.
func mergeFeatures(args []string) []string {
	var out, features []string
	for _, a := range args {
		v, ok := strings.CutPrefix(a, disableFeatures)
		if !ok {
			out = append(out, a)
			continue
		}
		for f := range strings.SplitSeq(v, ",") {
			if f != "" && !slices.Contains(features, f) {
				features = append(features, f)
			}
		}
	}
	if len(features) == 0 {
		return out
	}
	return append(out, disableFeatures+strings.Join(features, ","))
}

// policy is Chromium's managed policy, read from /etc/chromium/policies/managed, which the
// engine sees through the /etc bound over the real one.
var policy = map[string]any{
	"MetricsReportingEnabled":     false,
	"SafeBrowsingProtectionLevel": 0,
	"NetworkPredictionOptions":    2,
	"BrowserSignin":               0,
	"SyncDisabled":                true,
	"SpellcheckEnabled":           false,
	"TranslateEnabled":            false,
	"DnsOverHttpsMode":            "off",
	"BackgroundModeEnabled":       false,
	"ComponentUpdatesEnabled":     false,
	"PasswordManagerEnabled":      false,
}

const policyPath = "chromium/policies/managed/lanovo.json"

// writePolicy keeps the managed policy in the engine's /etc when it starts quiet, and removes it
// when it does not, so a policy from a quiet start does not outlive the choice.
func writePolicy(etc string, quieted bool) error {
	path := filepath.Join(etc, policyPath)
	if !quieted {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("engine: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	managed, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	if err := safe.WriteFile(path, managed, 0o644); err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	return nil
}

func writeEtc(dir string, nameservers []string) (string, error) {
	if len(nameservers) == 0 {
		return "", errors.New("engine: the device reports no nameservers")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("engine: %w", err)
	}
	var resolv strings.Builder
	for _, ns := range nameservers {
		fmt.Fprintf(&resolv, "nameserver %s\n", ns)
	}
	files := map[string]string{
		"resolv.conf":   resolv.String(),
		"hosts":         "127.0.0.1 localhost\n::1 localhost\n",
		"nsswitch.conf": "hosts: files dns\npasswd: files\ngroup: files\n",
	}
	for name, body := range files {
		if err := safe.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return "", fmt.Errorf("engine: %w", err)
		}
	}
	return dir, nil
}

func up(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

var (
	profileKeeps = []string{"Default", "Local State"}
	pageCaches   = []string{
		"Cache",
		"Code Cache",
		"GPUCache",
		"DawnWebGPUCache",
		"DawnGraphiteCache",
	}
)

func tidy(profile string) {
	entries, err := os.ReadDir(profile)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !slices.Contains(profileKeeps, e.Name()) {
			_ = os.RemoveAll(filepath.Join(profile, e.Name()))
		}
	}
	for _, name := range pageCaches {
		_ = os.RemoveAll(filepath.Join(profile, "Default", name))
	}
}

func wipe(dir, keep string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	for _, e := range entries {
		if e.Name() == keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("engine: %w", err)
		}
	}
	return nil
}
