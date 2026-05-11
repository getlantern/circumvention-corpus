#!/bin/bash
#
# Install the corpus-crawl + corpus-crawl-serve + corpus-findings-backfill
# launchd agents on the current machine.
#
# Why a script and not just `cp ... ~/Library/LaunchAgents/`: the plists
# in deploy/ have a hard-coded `/Users/afisk/code/circumvention-corpus`
# repo path (matches the mini's checkout). On other machines (laptops,
# fresh boxes, whoever-the-next-maintainer-is's machine) the checkout
# usually lives at `~/go/src/github.com/getlantern/circumvention-corpus`.
# This script substitutes the right path during install.
#
# Usage:
#   bash deploy/install-launchd.sh
#       Uses $(pwd) (or REPO env var if set) as the corpus path; installs
#       only the corpus-crawl plist (weekly cron). To install the other
#       two — serve mode + findings-backfill — pass them as args.
#
#   REPO=~/code/circumvention-corpus bash deploy/install-launchd.sh
#   bash deploy/install-launchd.sh corpus-crawl-serve corpus-findings-backfill
#
# Reload after upstream plist edits: re-run this script.

set -euo pipefail

REPO="${REPO:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
if [[ ! -x "$REPO/deploy/run-crawl.sh" ]]; then
    echo "error: $REPO/deploy/run-crawl.sh not found or not executable" >&2
    echo "       set REPO=... to point at your circumvention-corpus checkout" >&2
    exit 1
fi

AGENTS_DIR="$HOME/Library/LaunchAgents"
mkdir -p "$AGENTS_DIR"

# Default: just the weekly crawl. Caller can pass extra plist basenames.
plists=("io.lantern.corpus-crawl")
if [[ $# -gt 0 ]]; then
    plists=()
    for arg in "$@"; do
        case "$arg" in
            corpus-crawl)              plists+=("io.lantern.corpus-crawl") ;;
            corpus-crawl-serve|serve)  plists+=("io.lantern.corpus-crawl-serve") ;;
            corpus-findings-backfill|findings|backfill)
                                       plists+=("io.lantern.corpus-findings-backfill") ;;
            *)
                echo "unknown plist: $arg (want: corpus-crawl | serve | findings)" >&2
                exit 1
                ;;
        esac
    done
fi

# /Users/afisk/code/circumvention-corpus is the hard-coded path in the
# committed plist; substitute the actual REPO at install time.
SED_FROM='/Users/afisk/code/circumvention-corpus'

for label in "${plists[@]}"; do
    src="$REPO/deploy/$label.plist"
    dst="$AGENTS_DIR/$label.plist"
    if [[ ! -f "$src" ]]; then
        echo "skipping $label: $src not in repo (older repo version?)" >&2
        continue
    fi
    echo "installing $label → $dst (REPO=$REPO)"
    # Unload first if already loaded — silent on missing.
    launchctl unload "$dst" 2>/dev/null || true
    # Use a different sed delimiter since the path has slashes.
    sed "s|$SED_FROM|$REPO|g" "$src" > "$dst"
    launchctl load "$dst"
    echo "  loaded: $(launchctl list "$label" 2>/dev/null | head -1 || echo '(not visible)')"
done

echo
echo "Done. Logs go to /tmp/corpus-*.log on next firing."
echo "Trigger manually now (without waiting for schedule):"
for label in "${plists[@]}"; do
    echo "  launchctl start $label"
done
