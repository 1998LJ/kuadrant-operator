#!/usr/bin/env bash

# Validate upstream release branches, update tracked-branch mappings from
# release.yaml versions, then reconcile charts to their pinned refs.

set -euo pipefail

RELEASE_FILE="$env/release.yaml"
SYNC_FILE="$env/component-charts/sync.yaml"

errors=()

if [[ ! -f "$RELEASE_FILE" ]]; then
  echo "ERROR: $RELEASE_FILE not found"
  exit 1
fi

if [[ ! -f "$SYNC_FILE" ]]; then
  echo "ERROR: $SYNC_FILE not found"
  exit 1
fi

if ! command -v yq &>/dev/null; then
  echo "ERROR: yq is required but not found in PATH"
  exit 1
fi

# Check: every sync component must exist in release.yaml dependencies
while IFS= read -r component; do
  dep_version=$(yq ".dependencies.\"$component\" // \"\"" "$RELEASE_FILE")
  if [[ -z "$dep_version" ]]; then
    errors+=("Component '$component' is in $SYNC_FILE but missing from $RELEASE_FILE dependencies")
  fi
done < <(yq '.components | keys | .[]' "$SYNC_FILE")

if [[ ${#errors[@]} -gt 0 ]]; then
  echo ""
  echo "=============================="
  echo "ERRORS"
  echo "=============================="
  for err in "${errors[@]}"; do
    echo "  ERROR: $err"
  done
  exit 1
fi

# Preflight all derived upstream branches before changing sync.yaml. A
# missing branch must not leave earlier components partially updated.
while IFS= read -r component; do
  version=$(yq ".dependencies.\"$component\" // \"\"" "$RELEASE_FILE")
  if [[ -z "$version" || "$version" == "0.0.0" ]]; then
    continue
  fi

  if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    errors+=("Component '$component' has non-semver release version '$version'")
    continue
  fi

  expected_branch="release-${version%.*}"
  upstream_repo=$(yq ".components.\"$component\".repo // \"\"" "$SYNC_FILE")
  if [[ -z "$upstream_repo" ]]; then
    errors+=("Component '$component' has no upstream repo in $SYNC_FILE")
    continue
  fi

  if ! git ls-remote --quiet --exit-code --heads "https://github.com/${upstream_repo}.git" "refs/heads/${expected_branch}" >/dev/null; then
    errors+=("Cannot confirm upstream branch '$expected_branch' for '$component' in '$upstream_repo'")
  fi
done < <(yq '.components | keys | .[]' "$SYNC_FILE")

if [[ ${#errors[@]} -gt 0 ]]; then
  echo "ERROR: release chart branch validation failed; sync.yaml was not modified:" >&2
  for err in "${errors[@]}"; do
    echo "  $err" >&2
  done
  exit 1
fi

# Update tracked-branches in sync.yaml based on release.yaml versions
while IFS= read -r component; do
  version=$(yq ".dependencies.\"$component\" // \"\"" "$RELEASE_FILE")

  if [[ -z "$version" ]]; then
    echo "INFO: '$component' is in $SYNC_FILE but has no version in $RELEASE_FILE — skipping"
    continue
  fi

  if [[ "$version" == "0.0.0" ]]; then
    echo "WARN: '$component' version is 0.0.0 — tracked-branch not updated"
    continue
  fi

  expected_branch="release-${version%.*}"
  current_branch=$(yq ".components.\"$component\".tracked-branch" "$SYNC_FILE")

  if [[ "$current_branch" != "$expected_branch" ]]; then
    echo "Updating '$component' tracked-branch: '$current_branch' -> '$expected_branch'"
    yq -i ".components.\"$component\".tracked-branch = \"$expected_branch\"" "$SYNC_FILE"
  else
    echo "OK: '$component' tracked-branch already set to '$expected_branch'"
  fi
done < <(yq '.components | keys | .[]' "$SYNC_FILE")

# Warn about release.yaml deps not tracked in sync.yaml
while IFS='=' read -r dep version; do
  in_sync=$(yq ".components.\"$dep\" // \"\"" "$SYNC_FILE")
  if [[ -z "$in_sync" ]]; then
    echo "INFO: '$dep' ($version) is in $RELEASE_FILE but not tracked in $SYNC_FILE"
  fi
done < <(yq '.dependencies | to_entries | .[] | .key + "=" + .value' "$RELEASE_FILE")

echo ""
echo "Reconciling component charts to pinned refs..."
make -C "$env" sync-component-charts