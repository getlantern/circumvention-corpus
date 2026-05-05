// /api/ask — public-facing proxy that turns a free-form question into a
// cited LLM answer over the corpus.
//
// Request:  POST /api/ask  { "question": "...", "censors": [], ... }
// Response: { "question", "answer", "bundle", "elapsed_ms" }
//
// Architecture:
//   browser POST /api/ask  →  this Pages Function (rate-limit + auth)
//                          →  POST crawl.lantern.io/ask (CF Tunnel)
//                          →  corpus-crawl serve on the mini
//                          →  fetch synthesize bundle from corpus.lantern.io/mcp
//                          →  shell out to `claude -p` (auth via mini's keychain)
//                          →  return { question, answer, bundle }
//
// The bearer token to the tunnel lives in CF Pages env as
// CORPUS_CRAWL_TOKEN — never exposed to the browser. The browser hits
// this function anonymously; rate limiting per IP keeps quota bounded.
//
// Rate limit: each IP gets one request per ~12 seconds, refilling up to
// a 6-request burst, via Cloudflare's KV-style headers. We use the
// caller's IP (from cf-connecting-ip) and a Cloudflare KV-backed
// rolling-window counter when bound, otherwise fall through (degraded
// mode — only when the env binding is missing).

interface Env {
  CORPUS_CRAWL_TOKEN?: string;
  CRAWL_TUNNEL_URL?: string; // override for staging; defaults to crawl.lantern.io
  ASK_RATELIMIT?: KVNamespace;
}

const DEFAULT_TUNNEL = "https://crawl.lantern.io";
const RATE_WINDOW_SEC = 60 * 60; // 1 hour
const RATE_LIMIT_PER_WINDOW = 6;

function corsHeaders(): Record<string, string> {
  return {
    "Access-Control-Allow-Origin": "*",
    "Access-Control-Allow-Methods": "POST, OPTIONS",
    "Access-Control-Allow-Headers": "Content-Type",
  };
}

export const onRequestOptions: PagesFunction = () =>
  new Response(null, { status: 204, headers: corsHeaders() });

export const onRequestPost: PagesFunction<Env> = async (ctx) => {
  const env = ctx.env;
  const ip = ctx.request.headers.get("cf-connecting-ip") || "anon";

  if (!env.CORPUS_CRAWL_TOKEN) {
    return new Response(
      JSON.stringify({ error: "ask endpoint not configured (missing CORPUS_CRAWL_TOKEN)" }),
      { status: 503, headers: { "Content-Type": "application/json", ...corsHeaders() } },
    );
  }

  // Rate-limit if a KV binding is wired up.
  if (env.ASK_RATELIMIT) {
    const key = `ask:${ip}`;
    const cur = parseInt((await env.ASK_RATELIMIT.get(key)) || "0", 10);
    if (cur >= RATE_LIMIT_PER_WINDOW) {
      return new Response(
        JSON.stringify({
          error: `rate limit reached (${RATE_LIMIT_PER_WINDOW}/hr per IP); try again later`,
        }),
        {
          status: 429,
          headers: {
            "Content-Type": "application/json",
            "Retry-After": String(RATE_WINDOW_SEC),
            ...corsHeaders(),
          },
        },
      );
    }
    // Best-effort increment; if the put fails the window is just lossy.
    await env.ASK_RATELIMIT.put(key, String(cur + 1), { expirationTtl: RATE_WINDOW_SEC });
  }

  // Forward to the tunnel. The mini's corpus-crawl will do the heavy
  // lifting (synthesize → claude -p) and return JSON. We pass through
  // the upstream status so the UI can render fallback copy on 502/500.
  let body: unknown;
  try {
    body = await ctx.request.json();
  } catch (e) {
    return new Response(JSON.stringify({ error: "bad json" }), {
      status: 400,
      headers: { "Content-Type": "application/json", ...corsHeaders() },
    });
  }
  const tunnelURL = (env.CRAWL_TUNNEL_URL || DEFAULT_TUNNEL).replace(/\/+$/, "") + "/ask";
  let upstream: Response;
  try {
    upstream = await fetch(tunnelURL, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: "Bearer " + env.CORPUS_CRAWL_TOKEN,
      },
      body: JSON.stringify(body),
    });
  } catch (e) {
    return new Response(
      JSON.stringify({
        error: "ask backend unreachable",
        detail: (e as Error).message,
      }),
      {
        status: 502,
        headers: { "Content-Type": "application/json", ...corsHeaders() },
      },
    );
  }
  const text = await upstream.text();
  return new Response(text, {
    status: upstream.status,
    headers: {
      "Content-Type": upstream.headers.get("Content-Type") || "application/json",
      ...corsHeaders(),
    },
  });
};
