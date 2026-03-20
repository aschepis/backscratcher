#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ALIASES_FILE="$REPO_DIR/aliases.gitconfig"
GITCONFIG="$HOME/.gitconfig"
INCLUDE_LINE="path = $ALIASES_FILE"

# Check if already included
if grep -qF "$ALIASES_FILE" "$GITCONFIG" 2>/dev/null; then
  echo "✓ Git aliases already installed."
  exit 0
fi

echo "" >> "$GITCONFIG"
echo "[include]" >> "$GITCONFIG"
echo "  $INCLUDE_LINE" >> "$GITCONFIG"

echo "✓ Git aliases installed from $ALIASES_FILE"