// cmd_find_targets implements the "find-targets" subcommand: checks the
// default and eligible release branches for component-charts/sync.yaml entries
// tracking the dispatched component and source branch.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
)

var releaseTargetBranch = regexp.MustCompile(`^release-[0-9]+\.[0-9]+$`)

type targetBranch struct {
	Branch    string `json:"branch"`
	AutoMerge bool   `json:"auto-merge"`
}

// eligibleTargetBranches includes the default branch and only final release
// lines. Excluding prereleases and old release-v branches avoids dispatching
// unexpected syncs to branches not participating in the current workflow.
func eligibleTargetBranches(defaultBranch string, branches []string) []string {
	eligible := []string{defaultBranch}
	seen := map[string]bool{defaultBranch: true}
	releases := []string{}
	for _, branch := range branches {
		if !seen[branch] && releaseTargetBranch.MatchString(branch) {
			releases = append(releases, branch)
			seen[branch] = true
		}
	}
	sort.Strings(releases)
	return append(eligible, releases...)
}

// findMatchingTargets checks each eligible branch independently. A branch
// without sync.yaml has not opted into chart synchronization and is skipped.
func findMatchingTargets(repo, defaultBranch, componentName, sourceBranch string, branches []string,
	fetch func(string, string, string) ([]byte, error)) ([]targetBranch, error) {
	matches := []targetBranch{}
	for _, branch := range eligibleTargetBranches(defaultBranch, branches) {
		data, err := fetch(repo, "component-charts/sync.yaml", branch)
		if err != nil {
			var apiErr *ghAPIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
				continue
			}
			return nil, fmt.Errorf("fetching sync.yaml from %s@%s: %w", repo, branch, err)
		}
		cfg, err := loadConfigFromBytes(data)
		if err != nil {
			return nil, fmt.Errorf("parsing sync.yaml from %s@%s: %w", repo, branch, err)
		}
		if component, ok := cfg.Components[componentName]; ok && component.TrackedBranch == sourceBranch {
			matches = append(matches, targetBranch{Branch: branch, AutoMerge: component.AutoMerge})
		}
	}
	return matches, nil
}

func cmdFindTargets(args []string) {
	fs := flag.NewFlagSet("find-targets", flag.ExitOnError)
	repo := fs.String("repo", "", "GitHub owner/repo to search (e.g. Kuadrant/kuadrant-operator)")
	componentName := fs.String("component", "", "Component name to look up in sync.yaml (e.g. dns-operator)")
	sourceBranch := fs.String("source-branch", "", "The upstream branch that changed (e.g. main). "+
		"Matched against the tracked-branch field in sync.yaml")
	fs.Parse(args)
	if *repo == "" || *componentName == "" || *sourceBranch == "" {
		fmt.Fprintln(os.Stderr, "Error: --repo, --component, and --source-branch are required")
		os.Exit(1)
	}
	defaultBranch, err := getDefaultBranch(*repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting default branch: %v\n", err)
		os.Exit(1)
	}
	branches, err := getRepoBranches(*repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing repository branches: %v\n", err)
		os.Exit(1)
	}
	matches, err := findMatchingTargets(*repo, defaultBranch, *componentName, *sourceBranch, branches, getFileContent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding matching target branches: %v\n", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(matches); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing results: %v\n", err)
		os.Exit(1)
	}
}
