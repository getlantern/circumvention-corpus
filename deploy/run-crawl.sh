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
#   4. `go install` corpus-crawl + corpus-findings into $GOBIN. go
#      install handles atomic rename internally and uses Go's build
#      cache, so unchanged code skips compilation. Failure → continue
#      with existing $GOBIN binary (broken commit on main shouldn't
#      take down the bot).
#   5. exec the binary with all args passed through. The bot's own
#      logic (rejection cache, PR creation, etc.) takes over from here.
#
# The job is update-proof: any merge to main lands in the next launchd
# fire automatically, without manual rebuild/reinstall. Same for code
# changes in any of the crawler's dependencies (go install resolves
# go.mod every run).
#
# Logs go to launchd's Standard{Out,Error}Path (set in the plist).

set -u

# Resolve real script directory even if called via symlink.
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO=$(cd "$SCRIPT_DIR/.." && pwd)

# Install location. Honor user-set GOBIN if present, else use the
# standard $HOME/go/bin (which is Go's default install dir when
# $GOBIN and $GOPATH/bin aren't explicitly set).
: "${GOBIN:=$HOME/go/bin}"
export GOBIN
BINARY="$GOBIN/corpus-crawl"

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

# 4. Install both binaries via `go install`. corpus-crawl shells out
# to corpus-findings for full-text findings extraction on each
# accepted paper, so both need to be current. go install writes to
# $GOBIN with an atomic rename internally, and Go's build cache means
# unchanged code skips compilation entirely.
stamp "installing corpus-crawl + corpus-findings → $GOBIN"
# Portable mktemp form: BSD mktemp (macOS) does NOT substitute X's
# when there is a suffix after them — `mktemp /tmp/x.XXXXXX.log`
# creates a literal file named "x.XXXXXX.log" and the next invocation
# collides. `mktemp -t PREFIX` works identically on BSD and GNU and
# avoids the footgun.
install_log=$(mktemp -t corpus-crawl-install)
if go install ./cmd/corpus-crawl/ ./cmd/corpus-findings/ 2>"$install_log"; then
    stamp "install ok"
    rm -f "$install_log"
else
    stamp "install FAILED — using existing binary if present"
    sed 's/^/    /' "$install_log"
    rm -f "$install_log"
fi

# Belt-and-suspenders: clean up legacy in-repo binaries from earlier
# `go build` wrappers. locateFindingsBinary checks $REPO first, and a
# stale in-repo copy would shadow the fresh $GOBIN one. .gitignore
# already excludes them.
rm -f "$REPO/corpus-crawl" "$REPO/corpus-findings"

# 5. Sanity: corpus-crawl binary must exist (corpus-findings is best-
# effort — its absence just disables findings extraction, doesn't kill
# the run).
if [[ ! -x "$BINARY" ]]; then
    stamp "no executable binary at $BINARY; aborting"
    exit 1
fi

stamp "exec: $BINARY $*"
exec "$BINARY" "$@"
