# sync-components

CLI tool for reconciling upstream component Helm charts into `component-charts/`. By default it downloads the commits pinned in `sync.yaml`; explicit advance operations update the pins from tracked branch HEADs. Outputs JSON results.

## Usage

```bash
# Via make targets
make sync-component-charts                          # reconcile all pinned components
make sync-component-chart COMPONENT=dns-operator    # reconcile one pinned component
make advance-component-chart COMPONENT=dns-operator # advance one component to tracked HEAD

# Direct invocation
go run ./hack/sync-components/ sync --config component-charts/sync.yaml
go run ./hack/sync-components/ sync --config component-charts/sync.yaml --component dns-operator
go run ./hack/sync-components/ sync --config component-charts/sync.yaml --component dns-operator --follow-tracked-branch
```

## Subcommands

### sync

Downloads the charts at their pinned `ref` SHAs, replacing local chart content when it differs. The default operation is idempotent and leaves `ref` unchanged; it does not resolve `tracked-branch` HEAD. Pass `--follow-tracked-branch` to explicitly advance the pin to that branch HEAD and update `sync.yaml`. Human-readable progress is written to stderr; JSON results are written to stdout.

```bash
go run ./hack/sync-components/ sync --config component-charts/sync.yaml --component dns-operator
```

When the chart has been updated with `--follow-tracked-branch`:
```text
Syncing dns-operator from Kuadrant/dns-operator@cda782a
  Previous: abc123def45
  Updated.
{
  "dns-operator": {
    "repo": "Kuadrant/dns-operator",
    "old-ref": "abc123def456789...",
    "new-ref": "cda782a01149775...",
    "changed": true,
    "status": "updated"
  }
}
```

When already up to date:
```text
Syncing dns-operator from Kuadrant/dns-operator@cda782a
  No changes (already at cda782a).
{
  "dns-operator": {
    "repo": "Kuadrant/dns-operator",
    "old-ref": "cda782a01149775...",
    "new-ref": "cda782a01149775...",
    "changed": false,
    "status": "up-to-date"
  }
}
```

### find-targets

Queries the GitHub API to find which branches of a repo have a `sync.yaml` that tracks a given component at a given source branch. Used by the `sync-component-chart` GitHub Actions workflow.

```bash
go run ./hack/sync-components/ find-targets \
    --repo Kuadrant/kuadrant-operator \
    --component dns-operator \
    --source-branch main
```

```json
[{"branch":"main","auto-merge":true}]
```

Returns `[]` when no branches track the given component at that source branch.

### query

Outputs the tracking configuration for a single component as JSON.

```bash
go run ./hack/sync-components/ query --config component-charts/sync.yaml --component dns-operator
```

```json
{"repo":"Kuadrant/dns-operator","chart-path":"charts/dns-operator","tracked-branch":"main","ref":"cda782a01149775b83062600c119971f45a657fb","auto-merge":true}
```

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `GH_TOKEN` | GitHub token for API authentication (avoids rate limiting) |
| `GITHUB_TOKEN` | Alternative to `GH_TOKEN` (set automatically in GitHub Actions) |

Authentication is optional for public repos but recommended to avoid the 60 requests/hour unauthenticated rate limit.
