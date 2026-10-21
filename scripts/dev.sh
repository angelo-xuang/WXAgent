#!/usr/bin/env bash
set -euo pipefail

task="${1:-check}"
component="${2:-all}"
case "$task" in check|format) ;; *) echo "Usage: bash scripts/dev.sh [check|format] [all|go|flutter]" >&2; exit 2 ;; esac
case "$component" in all|go|flutter) ;; *) echo "Unknown component: $component" >&2; exit 2 ;; esac
cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [[ "$component" == all || "$component" == go ]]; then
  command -v go >/dev/null
  command -v gofmt >/dev/null
  unformatted=0
  # Only first-party Go modules are included; vendored modules/ stays untouched.
  while IFS= read -r -d '' file; do
    if [[ "$task" == format ]]; then
      gofmt -w "$file"
    else
      result="$(gofmt -l "$file")"
      if [[ -n "$result" ]]; then
        printf '%s\n' "$result" >&2
        unformatted=1
      fi
    fi
  done < <(find backend launcher -type d -name vendor -prune -o -type f -name '*.go' -print0)
  if [[ "$unformatted" != 0 ]]; then
    echo "Go formatting differs. Run: bash scripts/dev.sh format go" >&2
    exit 1
  fi
  if [[ "$task" == check ]]; then
    for module in backend launcher; do
      (cd "$module"; go test ./...; go vet ./...)
    done
  fi
fi

if [[ "$component" == all || "$component" == flutter ]]; then
  command -v flutter >/dev/null
  command -v dart >/dev/null
  (
    cd frontend
    if [[ "$task" == format ]]; then
      dart format lib test
    else
      flutter pub get --enforce-lockfile
      dart format --output=none --set-exit-if-changed lib test
      flutter analyze --no-pub
      flutter test --no-pub
    fi
  )
fi
printf 'Completed: %s (%s).\n' "$task" "$component"
