package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHubTransport serves canned responses for the two GitHub endpoints
// syncOne depends on (commit resolution and tarball download), so tests can
// exercise the real sync logic without hitting the network.
type fakeGitHubTransport struct {
	sha      string
	tarball  []byte
	requests []string
}

func (f *fakeGitHubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	url := req.URL.String()
	f.requests = append(f.requests, url)
	switch {
	case strings.Contains(url, "/commits/"):
		body := fmt.Sprintf(`{"sha":%q}`, f.sha)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	case strings.Contains(url, "/tarball/"):
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(f.tarball)),
			Header:     make(http.Header),
		}, nil
	default:
		return nil, fmt.Errorf("fakeGitHubTransport: unexpected request %s", url)
	}
}

// writeSyncConfig writes a minimal single-component sync.yaml and returns the
// loaded component alongside the config file path.
func writeSyncConfig(t *testing.T, dir, name, ref string) (component, string) {
	t.Helper()
	configPath := filepath.Join(dir, "sync.yaml")
	content := fmt.Sprintf(`components:
  %s:
    repo: Kuadrant/%s
    chart-path: charts/%s
    tracked-branch: main
    ref: %s
    auto-merge: false
`, name, name, name, ref)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	return cfg.Components[name], configPath
}

// TestSyncOne_MissingChartDirectory verifies the fix for a bug where syncOne
// would report "up-to-date" and skip downloading whenever the resolved SHA
// already matched sync.yaml's ref -- even if the local chart directory was
// missing (e.g. the component was only just uncommented, or its chart was
// deleted). It must always download when the local directory is absent.
func TestSyncOne_MissingChartDirectory(t *testing.T) {
	sha := "abc123def4567890abc123def4567890abc123d"
	tarball := buildTestTarball(t, map[string]string{
		"Kuadrant-widget-abc123d/charts/widget/Chart.yaml":  "apiVersion: v2\nname: widget",
		"Kuadrant-widget-abc123d/charts/widget/values.yaml": "# empty",
	})

	origClient := httpClient
	httpClient = &http.Client{Transport: &fakeGitHubTransport{sha: sha, tarball: tarball}}
	defer func() { httpClient = origClient }()

	dir := t.TempDir()
	c, configPath := writeSyncConfig(t, dir, "widget", sha)

	// The chart directory does not exist on disk even though c.Ref already
	// equals the SHA that will be resolved -- this is exactly the scenario
	// that used to be silently skipped.
	result := syncOne("widget", c, configPath, false)

	if result.Status == "up-to-date" {
		t.Fatal("expected chart to be downloaded when local directory is missing, got up-to-date")
	}
	if result.Status == "error" {
		t.Fatalf("unexpected error status: %+v", result)
	}

	chartDir := filepath.Join(dir, "widget")
	if _, err := os.Stat(filepath.Join(chartDir, "Chart.yaml")); err != nil {
		t.Fatalf("expected chart to be downloaded to %s: %v", chartDir, err)
	}

	info, err := os.Stat(chartDir)
	if err != nil {
		t.Fatalf("stat chart dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("chart dir permission = %v, want 0755", got)
	}
}

// TestSyncOne_UpToDateWhenChartPresent verifies a pinned chart is up-to-date
// only when its content matches the downloaded chart, not merely when the
// directory exists and the pinned SHA is unchanged.
func TestSyncOne_UpToDateWhenChartPresent(t *testing.T) {
	sha := "abc123def4567890abc123def4567890abc123d"

	tarball := buildTestTarball(t, map[string]string{
		"Kuadrant-widget-abc123d/charts/widget/Chart.yaml": "apiVersion: v2",
	})
	origClient := httpClient
	httpClient = &http.Client{Transport: &fakeGitHubTransport{sha: sha, tarball: tarball}}
	defer func() { httpClient = origClient }()

	dir := t.TempDir()
	c, configPath := writeSyncConfig(t, dir, "widget", sha)

	chartDir := filepath.Join(dir, "widget")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("apiVersion: v2"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := syncOne("widget", c, configPath, false)
	if result.Status != "up-to-date" {
		t.Fatalf("expected up-to-date, got %+v", result)
	}
}

// TestSyncOne_ReconcilesPinnedRef verifies the default mode reads the pinned
// SHA, not the tracked branch HEAD, even when that branch is configured.
func TestSyncOne_ReconcilesPinnedRef(t *testing.T) {
	sha := "abc123def4567890abc123def4567890abc123d"
	tarball := buildTestTarball(t, map[string]string{
		"Kuadrant-widget-abc123d/charts/widget/Chart.yaml": "apiVersion: v2\nname: widget",
	})
	fake := &fakeGitHubTransport{sha: sha, tarball: tarball}
	origClient := httpClient
	httpClient = &http.Client{Transport: fake}
	defer func() { httpClient = origClient }()
	dir := t.TempDir()
	c, configPath := writeSyncConfig(t, dir, "widget", sha)
	chartDir := filepath.Join(dir, "widget")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("manually changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := syncOne("widget", c, configPath, false)
	if result.Status != "updated" || !result.Changed || result.NewRef != sha {
		t.Fatalf("reconciliation result = %+v", result)
	}
	restored, err := os.ReadFile(filepath.Join(chartDir, "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != "apiVersion: v2\nname: widget" {
		t.Fatalf("chart content not restored: %s", restored)
	}
	config, err := loadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.Components["widget"].Ref != sha {
		t.Fatal("reconciliation rewrote pinned SHA")
	}
	for _, request := range fake.requests {
		if strings.Contains(request, "/commits/main") {
			t.Fatalf("reconcile contacted tracked branch: %s", request)
		}
	}
	if len(fake.requests) != 2 || !strings.Contains(fake.requests[0], "/commits/"+sha) {
		t.Fatalf("unexpected GitHub requests: %v", fake.requests)
	}
}

func TestSyncOne_AdvanceMode(t *testing.T) {
	oldSHA := "abc123def4567890abc123def4567890abc123d"
	newSHA := "def123abc4567890def123abc4567890def123a"
	tarball := buildTestTarball(t, map[string]string{
		"Kuadrant-widget-def123a/charts/widget/Chart.yaml": "apiVersion: v2\nname: widget-v2",
	})
	fake := &fakeGitHubTransport{sha: newSHA, tarball: tarball}
	origClient := httpClient
	httpClient = &http.Client{Transport: fake}
	defer func() { httpClient = origClient }()

	dir := t.TempDir()
	c, configPath := writeSyncConfig(t, dir, "widget", oldSHA)
	result := syncOne("widget", c, configPath, true)
	if result.Status != "updated" || !result.Changed || result.OldRef != oldSHA || result.NewRef != newSHA {
		t.Fatalf("advance result = %+v", result)
	}
	config, err := loadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.Components["widget"].Ref != newSHA {
		t.Fatalf("ref not advanced: %s", config.Components["widget"].Ref)
	}
	if len(fake.requests) != 2 || !strings.Contains(fake.requests[0], "/commits/main") || !strings.Contains(fake.requests[1], "/tarball/"+newSHA) {
		t.Fatalf("advance used wrong refs: %v", fake.requests)
	}
}

func TestSyncRefModes(t *testing.T) {
	c := component{Ref: "pinned", TrackedBranch: "release-0.18"}
	if got, err := syncRef(c, false); err != nil || got != "pinned" {
		t.Fatalf("reconcile ref: %q %v", got, err)
	}
	if got, err := syncRef(c, true); err != nil || got != "release-0.18" {
		t.Fatalf("advance ref: %q %v", got, err)
	}
	c.TrackedBranch = ""
	if _, err := syncRef(c, true); err == nil {
		t.Fatal("advance without tracked-branch should fail")
	}
}

// TestSyncOne_AdvanceRefWithoutContentDiff verifies advance mode can update
// the pin without rewriting vendored chart content when the chart itself is
// unchanged between the upstream commits.
func TestSyncOne_AdvanceRefWithoutContentDiff(t *testing.T) {
	oldSHA := "abc123def4567890abc123def4567890abc123d"
	newSHA := "def123abc4567890def123abc4567890def123a"
	tarball := buildTestTarball(t, map[string]string{
		"Kuadrant-widget-def123a/charts/widget/Chart.yaml": "apiVersion: v2",
	})
	fake := &fakeGitHubTransport{sha: newSHA, tarball: tarball}
	origClient := httpClient
	httpClient = &http.Client{Transport: fake}
	defer func() { httpClient = origClient }()
	dir := t.TempDir()
	c, configPath := writeSyncConfig(t, dir, "widget", oldSHA)
	chartDir := filepath.Join(dir, "widget")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("apiVersion: v2"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := syncOne("widget", c, configPath, true)
	if result.Status != "ref-updated" || !result.Changed || result.NewRef != newSHA {
		t.Fatalf("expected ref-only advance, got %+v", result)
	}
	config, err := loadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.Components["widget"].Ref != newSHA {
		t.Fatalf("ref not advanced: %s", config.Components["widget"].Ref)
	}
	content, err := os.ReadFile(filepath.Join(chartDir, "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "apiVersion: v2" {
		t.Fatalf("chart unexpectedly changed: %q", content)
	}
}