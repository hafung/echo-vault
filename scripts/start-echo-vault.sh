#!/usr/bin/env bash
# Compatibility entry for existing shortcuts.
set -euo pipefail
exec bash "$(dirname -- "${BASH_SOURCE[0]}")/start-gpt-vault.sh" "$@"
