#!/bin/bash
#
# Self-updating wrapper for corpus-crawl. Called by launchd in place of
# invoking the binary directly. Each fire of the cron does:
#
#   1. Recover to main (in case a previous run left us on an auto-ingest
#      branch — gh pr create followed by `git checkout main` is best-
#      effort, and a crashed process leaves an orphan).
#   2. Garbage-collect old local auto-ingest branches.
#   3. git pull --ff-only main. Failure → continue with existing tree
#      (offline run still works against last-known-good source).
#   4. go build into a tempfile, then atomically rename. Failure →
#      continue with existing binary (broken commit on main shouldn't
#      take down the bot).
#   5. exec the binary with all args passed through. The bot's own
#      logic (rejection cache, PR creation, etc.) takes over from here.
#
# Logs go to launchd's Standard{Out,Error}Path (set in the plist).

set -u

# Resolve real script directory even if called via symlink.
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO=$(cd "$SCRIPT_DIR/.." && pwd)
BINARY="$REPO/corpus-crawl"

cd "$REPO" || { echo "wrapper: cannot cd to $REPO; abort" >&2; exit 1; }

stamp() { echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] wrapper:" "$@"; }

# 1. Recover to main.
current_branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
if [[ "$current_branch" != "main" ]]; then
    stamp "recovering from branch '$current_branch' → main"
    git checkout --quiet main 2>&1 || stamp "checkout main failed (continuing)"
fi

# 2. Garbage-collect old auto-ingest branches that gh pr create left
# behind. These can't be on main; safe to delete locally. Don't fail
# if there are none.
auto_branches=$(git branch --list 'auto-ingest/*' | tr -d ' *')
if [[ -n "$auto_branches" ]]; then
    stamp "deleting stale auto-ingest branches: $(echo "$auto_branches" | wc -l | tr -d ' ')"
    echo "$auto_branches" | xargs -r git branch -D >/dev/null 2>&1 || true
fi

# 3. Pull. Fast-forward only — refuses to apply if there's local
# divergence, which would surface a real bug rather than silently
# papering over it.
stamp "pulling latest"
if git pull --ff-only --quiet origin main 2>&1; then
    stamp "pull ok @ $(git rev-parse --short HEAD)"
else
    stamp "pull FAILED; using existing tree @ $(git rev-parse --short HEAD)"
fi

# 4. Build both binaries into tempfiles, atomically rename. corpus-crawl
# shells out to corpus-findings for full-text findings extraction on
# each accepted paper, so both need to be current.
build_log=$(mktemp /tmp/corpus-crawl-build.XXXXXX.log)
build_one() {
    local pkg="$1" target="$2"
    if go build -o "$target.new" "$pkg" 2>>"$build_log"; then
        mv -f "$target.new" "$target"
        chmod +x "$target"
        return 0
    fi
    rm -f "$target.new"
    return 1
}

stamp "building corpus-crawl + corpus-findings"
ok=1
for pair in "./cmd/corpus-crawl|$REPO/corpus-crawl" "./cmd/corpus-findings|$REPO/corpus-findings"; do
    pkg="${pair%%|*}"; target="${pair##*|}"
    if ! build_one "$pkg" "$target"; then
        stamp "build FAILED for $pkg — using existing binary if present"
        ok=0
    fi
done
if [[ $ok -eq 1 ]]; then
    stamp "build ok"
    rm -f "$build_log"
else
    stamp "Build log:"
    sed 's/^/    /' "$build_log"
    rm -f "$build_log"
fi

# 5. Sanity: corpus-crawl binary must exist (corpus-findings is best-
# effort — its absence just disables findings extraction, doesn't kill
# the run).
if [[ ! -x "$BINARY" ]]; then
    stamp "no executable binary at $BINARY; aborting"
    exit 1
fi

stamp "exec: $BINARY $*"
exec "$BINARY" "$@"
