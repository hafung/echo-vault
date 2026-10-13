#!/usr/bin/env bash
set -euo pipefail
project="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$project"
if [[ ! -x ./bin/echo-vault ]]; then
  printf '%s\n' 'Build the application with pnpm build first.' >&2
  exit 1
fi
exec ./bin/echo-vault --open-browser "$@"
