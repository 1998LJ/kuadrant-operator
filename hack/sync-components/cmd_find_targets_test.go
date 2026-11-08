package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func configForTrackedBranch(branch string, autoMerge bool) []byte {
	return []byte(fmt.Sprintf(`components:
  dns-operator:
    repo: Kuadrant/dns-operator
    chart-path: charts/dns-operator
    tracked-branch: %s
    ref: abc123
    auto-merge: %t
`, branch, autoMerge))
}

func TestEligibleTargetBranches(t *testing.T) {
	branches := []string{
		"release-v1.6", "release-1.6.0-rc4", "release-1.6", "main",
		"release-1.4", "release-workflow-update", "release-1.6",
		"release-0.9", "release-1.5", "feature/other",
	}
	got := eligibleTargetBranches("main", branches)
	want := []string{"main", "release-0.9", "release-1.4", "release-1.5", "release-1.6"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("eligible branches = %v, want %v", got, want)
	}
}

func TestFindMatchingTargets(t *testing.T) {
	configs := map[string][]byte{
		"main":        configForTrackedBranch("main", true),
		"release-1.6": configForTrackedBranch("release-0.18", false),
		"release-1.5": nil, // This branch predates the sync tooling.
	}
	branches := []string{"release-1.6", "release-1.5", "main", "release-v1.4"}
	for _, tc := range []struct {
		name   string
		source string
		want   []targetBranch
	}{
		{"main dispatch", "main", []targetBranch{{Branch: "main", AutoMerge: true}}},
		{"release dispatch", "release-0.18", []targetBranch{{Branch: "release-1.6", AutoMerge: false}}},
		{"untracked dispatch", "release-0.99", []targetBranch{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requested := map[string]int{}
			fetch := func(repo, path, ref string) ([]byte, error) {
				if repo != "Kuadrant/kuadrant-operator" || path != "component-charts/sync.yaml" {
					t.Fatalf("unexpected lookup: %s %s", repo, path)
				}
				requested[ref]++
				if data := configs[ref]; data != nil {
					return data, nil
				}
				return nil, &ghAPIError{StatusCode: 404, URL: ref}
			}
			got, err := findMatchingTargets("Kuadrant/kuadrant-operator", "main", "dns-operator", tc.source, branches, fetch)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("targets = %v, want %v", got, tc.want)
			}
			for _, branch := range []string{"main", "release-1.5", "release-1.6"} {
				if requested[branch] != 1 {
					t.Errorf("branch %s read %d times, want 1", branch, requested[branch])
				}
			}
			if requested["release-v1.4"] != 0 {
				t.Fatal("unexpected legacy release lookup")
			}
		})
	}
}

func TestFindMatchingTargetsErrors(t *testing.T) {
	branches := []string{"release-1.6"}
	for _, tc := range []struct {
		name    string
		fetch   func(string, string, string) ([]byte, error)
		wantErr string
		want    []targetBranch
	}{
		{
			name: "missing default branch config is skipped",
			fetch: func(_, _, ref string) ([]byte, error) {
				if ref == "main" {
					return nil, &ghAPIError{StatusCode: 404}
				}
				return configForTrackedBranch("release-0.18", false), nil
			},
			want: []targetBranch{{Branch: "release-1.6", AutoMerge: false}},
		},
		{
			name:    "non-404 lookup error is returned",
			fetch:   func(_, _, _ string) ([]byte, error) { return nil, &ghAPIError{StatusCode: 403} },
			wantErr: "fetching sync.yaml from Kuadrant/kuadrant-operator@main: HTTP 403",
		},
		{
			name:    "invalid config fails loudly",
			fetch:   func(_, _, _ string) ([]byte, error) { return []byte("components: []"), nil },
			wantErr: "parsing sync.yaml from Kuadrant/kuadrant-operator@main",
		},
		{
			name: "wrapped missing config is skipped",
			fetch: func(_, _, ref string) ([]byte, error) {
				if ref == "main" {
					return nil, fmt.Errorf("wrapped: %w", &ghAPIError{StatusCode: 404})
				}
				return configForTrackedBranch("release-0.18", false), nil
			},
			want: []targetBranch{{Branch: "release-1.6", AutoMerge: false}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := findMatchingTargets("Kuadrant/kuadrant-operator", "main", "dns-operator", "release-0.18", branches, tc.fetch)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("targets = %v, want %v", got, tc.want)
			}
		})
	}
}