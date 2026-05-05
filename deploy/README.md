# Deploying the corpus-crawl bot

These artifacts deploy `cmd/corpus-crawl` on the Mac mini at `afisk@mini`. They handle two paths:

- **launchd cron** — runs `corpus-crawl run` every Monday at 10:00 local time
- **launchd serve + Cloudflare Tunnel** — keeps `corpus-crawl serve` listening on `127.0.0.1:8788`, exposed at `https://crawl.lantern.io/crawl` for on-demand triggers

The crawler runs on the mini (not GitHub Actions) so:
- the source IP is residential (arXiv / USENIX / NDSS routinely throttle datacenter IPs)
- `wick` is available for browser-grade HTML scraping of conference pages
- `claude -p` reuses your local Claude subscription instead of needing an API-key secret in CI
- local Claude has access to your MCP servers (including corpus-mcp itself), so future versions can do real corpus dedup via MCP rather than fragile title comparison

## One-time setup on the mini

SSH in and do these interactively (they require browser-based auth flows that can't be scripted):

```bash
ssh afisk@mini

# 1. Install dependencies (the laptop has already done this — skip if green)
brew install go gh cloudflared yq
npm install -g @anthropic-ai/claude-code

# 2. Authenticate Claude Code (browser opens for OAuth)
claude
# At the prompt: type /login, complete the browser flow, then exit with /exit
echo "test" | claude -p   # should produce a response

# 3. Authenticate gh CLI
gh auth login -h github.com -w
# choose: HTTPS, yes (auth git ops), Login with a web browser
gh auth status            # should show ✓

# 4. Clone the corpus repo
mkdir -p ~/code
cd ~/code
git clone https://github.com/getlantern/circumvention-corpus
cd circumvention-corpus

# 5. Build the binary in-tree
go build ./cmd/corpus-crawl
./corpus-crawl run --dry-run --max 3 --window-days 7    # smoke test

# 6. Install the launchd cron agent
mkdir -p ~/Library/LaunchAgents
cp deploy/io.lantern.corpus-crawl.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/io.lantern.corpus-crawl.plist

# 7. Verify it's loaded
launchctl list | grep corpus-crawl

# 8. (Optional) trigger one run immediately to confirm end-to-end
launchctl start io.lantern.corpus-crawl
sleep 30
tail -50 /tmp/corpus-crawl.out.log /tmp/corpus-crawl.err.log
```

After the first run produces a PR, review and merge it. Future runs will fire automatically every Monday.

## Optional: Cloudflare Tunnel (on-demand triggers)

If you want to be able to trigger crawls from anywhere — phone, laptop, a deployed dashboard — add the tunnel:

```bash
ssh afisk@mini

# 1. Install cloudflared (already done)
# 2. Authenticate cloudflared (opens a browser to authorize the tunnel)
cloudflared tunnel login

# 3. Create the tunnel
cloudflared tunnel create corpus-crawl
# Note the tunnel UUID and credentials-file path it prints.

# 4. Edit deploy/cloudflared-config.yml with your tunnel UUID, then install
mkdir -p ~/.cloudflared
sed -e "s/REPLACE_WITH_TUNNEL_UUID/<your-tunnel-uuid>/g" \
    deploy/cloudflared-config.yml > ~/.cloudflared/config.yml

# 5. Route DNS so crawl.lantern.io points at the tunnel
cloudflared tunnel route dns corpus-crawl crawl.lantern.io

# 6. Generate a shared bearer token and store it
TOKEN=$(openssl rand -hex 24)
echo "$TOKEN" > ~/.cloudflared/corpus-crawl-token   # save somewhere safe
launchctl setenv CORPUS_CRAWL_TOKEN "$TOKEN"

# 7. Install the serve-mode launchd agent
cp deploy/io.lantern.corpus-crawl-serve.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/io.lantern.corpus-crawl-serve.plist

# 8. Start cloudflared as a background service
brew services start cloudflared

# 9. Test the tunnel
curl https://crawl.lantern.io/healthz                      # should return "ok"
curl -X POST \
     -H "Authorization: Bearer $TOKEN" \
     "https://crawl.lantern.io/crawl?max=3&dry_run=1"
```

## Updating the bot

The cron-mode launchd agent self-updates: `deploy/run-crawl.sh` does
`git pull --ff-only` + `go build` before exec'ing the binary on every
firing. Code changes pushed to `main` land in the next Monday's run
automatically. If pull or build fails, the wrapper falls back to the
last-known-good binary so a broken commit doesn't take the bot down.

To force an immediate update + run (rather than waiting for the next
scheduled firing):

```bash
ssh afisk@mini 'launchctl start io.lantern.corpus-crawl'
```

The serve-mode agent does NOT self-update on its own (it's a long-
running process). Restart it to pick up new code:

```bash
ssh afisk@mini 'launchctl kickstart -k gui/$(id -u)/io.lantern.corpus-crawl-serve'
```

If you change either plist file (e.g., to tweak the schedule or the
launch arguments), the new plist itself has to be redeployed manually:

```bash
ssh afisk@mini
cd ~/code/circumvention-corpus && git pull
cp deploy/io.lantern.corpus-crawl.plist ~/Library/LaunchAgents/
launchctl unload ~/Library/LaunchAgents/io.lantern.corpus-crawl.plist
launchctl load   ~/Library/LaunchAgents/io.lantern.corpus-crawl.plist
```

## Logs

- Cron mode: `/tmp/corpus-crawl.{out,err}.log`
- Serve mode: `/tmp/corpus-crawl-serve.{out,err}.log`
- cloudflared: `brew services info cloudflared` will tell you where its log file is

## Uninstall

```bash
launchctl unload ~/Library/LaunchAgents/io.lantern.corpus-crawl.plist
launchctl unload ~/Library/LaunchAgents/io.lantern.corpus-crawl-serve.plist
rm ~/Library/LaunchAgents/io.lantern.corpus-crawl*.plist
brew services stop cloudflared
cloudflared tunnel delete corpus-crawl   # if you set up the tunnel
```
