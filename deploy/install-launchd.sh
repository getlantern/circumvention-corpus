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
# corpus-crawl serve mode needs CORPUS_CRAWL_TOKEN. If a token file
# exists at ~/.config/lantern/corpus-crawl-token (or $REPO/deploy/.corpus-crawl-token,
# or $TOKEN_FILE), this script injects the token into the rendered
# plist's EnvironmentVariables. Without that file, you get a runtime
# warning and the serve agent will refuse to start until you provide one.
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

# Two path substitutions happen at install time:
#   1. /Users/afisk/code/circumvention-corpus → $REPO
#      (the corpus checkout; defaults to the dir containing this script)
#   2. /Users/afisk/go/bin → $GOBIN
#      (the binary install dir from `go install`; defaults to $HOME/go/bin)
SED_REPO='/Users/afisk/code/circumvention-corpus'
SED_GOBIN='/Users/afisk/go/bin'
: "${GOBIN:=$HOME/go/bin}"

# corpus-crawl serve mode requires CORPUS_CRAWL_TOKEN to be set in the
# LaunchAgent's environment. The committed plist deliberately doesn't
# include it (the token is a per-machine secret that mustn't enter
# git). Historically we relied on `launchctl setenv CORPUS_CRAWL_TOKEN
# "..."` from a Terminal.app on the host, but that's brittle:
#   - The value is per-launchd-domain; setenv from SSH targets the
#     wrong domain (user/<uid> vs gui/<uid>) and is invisible to the
#     LaunchAgent.
#   - The value evaporates on every reboot.
# Both bit us on 2026-05-22 and led to a multi-hour outage.
#
# Now: if $TOKEN_FILE exists (default $HOME/.config/lantern/corpus-crawl-token
# or $REPO/deploy/.corpus-crawl-token, in that order), read its first
# line as the token and bake it into the rendered plist's
# EnvironmentVariables dict via plutil. The committed plist stays
# secret-free; the local rendered copy has the token in-place; reboots
# preserve it (it lives in the rendered plist, not in launchctl setenv).
: "${TOKEN_FILE:=}"
if [[ -z "$TOKEN_FILE" ]]; then
    for candidate in "$HOME/.config/lantern/corpus-crawl-token" "$REPO/deploy/.corpus-crawl-token"; do
        if [[ -f "$candidate" ]]; then
            TOKEN_FILE="$candidate"
            break
        fi
    done
fi
TOKEN_VALUE=""
if [[ -n "$TOKEN_FILE" && -f "$TOKEN_FILE" ]]; then
    TOKEN_VALUE="$(head -n 1 "$TOKEN_FILE" | tr -d '[:space:]')"
    if [[ -n "$TOKEN_VALUE" ]]; then
        echo "  (token file found: $TOKEN_FILE — will inject into corpus-crawl-serve)"
    fi
fi

for label in "${plists[@]}"; do
    src="$REPO/deploy/$label.plist"
    dst="$AGENTS_DIR/$label.plist"
    if [[ ! -f "$src" ]]; then
        echo "skipping $label: $src not in repo (older repo version?)" >&2
        continue
    fi
    echo "installing $label → $dst (REPO=$REPO, GOBIN=$GOBIN)"
    # Unload first if already loaded — silent on missing.
    launchctl unload "$dst" 2>/dev/null || true
    # Use a different sed delimiter since the paths contain slashes.
    sed -e "s|$SED_REPO|$REPO|g" \
        -e "s|$SED_GOBIN|$GOBIN|g" \
        "$src" > "$dst"
    # Token injection for serve mode. plutil -insert is idempotent for
    # missing keys; if the key happens to exist already, fall back to
    # -replace.
    if [[ "$label" == "io.lantern.corpus-crawl-serve" && -n "$TOKEN_VALUE" ]]; then
        if ! /usr/bin/plutil -insert EnvironmentVariables.CORPUS_CRAWL_TOKEN -string "$TOKEN_VALUE" "$dst" 2>/dev/null; then
            /usr/bin/plutil -replace EnvironmentVariables.CORPUS_CRAWL_TOKEN -string "$TOKEN_VALUE" "$dst"
        fi
        echo "  (CORPUS_CRAWL_TOKEN baked into plist)"
    fi
    launchctl load "$dst"
    echo "  loaded: $(launchctl list "$label" 2>/dev/null | head -1 || echo '(not visible)')"
done

if [[ -n "${plists[*]:-}" && " ${plists[*]} " == *" io.lantern.corpus-crawl-serve "* && -z "$TOKEN_VALUE" ]]; then
    echo
    echo "WARNING: corpus-crawl-serve was installed without a CORPUS_CRAWL_TOKEN."
    echo "         It will crash on first start with --auth-token errors."
    echo "         Create a token file with:"
    echo "             mkdir -p ~/.config/lantern"
    echo "             echo '<your token>' > ~/.config/lantern/corpus-crawl-token"
    echo "             chmod 600 ~/.config/lantern/corpus-crawl-token"
    echo "         then re-run this script."
fi

echo
echo "Done. Logs go to /tmp/corpus-*.log on next firing."
echo "Trigger manually now (without waiting for schedule):"
for label in "${plists[@]}"; do
    echo "  launchctl start $label"
done
