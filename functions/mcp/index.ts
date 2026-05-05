// MCP server for the circumvention corpus, served at corpus.lantern.io/mcp.
// Implements the Streamable HTTP transport from the MCP spec — clients POST
// JSON-RPC, we return JSON. No SSE streaming (our 4 tools all reply in one
// shot). Same 5 tools as cmd/corpus-mcp's stdio version: search_papers,
// get_paper, list_taxonomy, find_related, synthesize.
//
// The corpus is bundled into the Worker at build time by cmd/corpus-bundle
// (Go) → functions/_data/corpus.json. Wrangler/esbuild inlines the JSON,
// so there's no runtime fetch.

import corpusData from "../_data/corpus.json";

interface Paper {
  id: string;
  title: string;
  authors?: string[];
  venue?: string;
  year: number;
  doi?: string;
  arxiv_id?: string;
  url?: string;
  abstract?: string;
  censors: string[];
  techniques: string[];
  defenses_discussed?: string[];
  defenses_evaluated_against?: string[];
  evaluation_methods?: string[];
  core?: boolean;
  notes?: string;
  visibility: string;
  references?: string[];
}

interface Finding {
  id?: string;
  paper: string;
  kind: string;
  summary: string;
  techniques?: string[];
  defenses?: string[];
  censors?: string[];
  defense_implications?: string[];
  section?: string;
  extracted_by?: string;
  human_validated_by?: string;
}

interface Bundle {
  generated: string;
  papers: Paper[];
  findings: Finding[];
  taxonomy: Record<string, unknown>;
}

const CORPUS = corpusData as unknown as Bundle;
const BY_ID = new Map<string, Paper>(CORPUS.papers.map((p) => [p.id, p]));

// Pre-built map of paper-id → concatenated findings text. Used by the
// search haystack so a query like "blocking iran 443" matches a paper
// whose abstract is generic but whose extracted findings document the
// specific event. This is what makes the 109+ findings carry their
// own search weight rather than only being visible after get_paper.
const FINDINGS_TEXT_BY_PAPER = new Map<string, string>();
for (const f of CORPUS.findings) {
  if (!f.paper) continue;
  const text = [
    f.summary || "",
    (f.defense_implications || []).join(" "),
    f.section || "",
  ].join(" ");
  const existing = FINDINGS_TEXT_BY_PAPER.get(f.paper) || "";
  FINDINGS_TEXT_BY_PAPER.set(f.paper, existing + " " + text);
}

const SERVER_INFO = { name: "circumvention-corpus", version: "0.3.0" };
const PROTOCOL_VERSION = "2024-11-05";

const TOOLS = [
  {
    name: "search_papers",
    description:
      "Keyword + tag-filter search over the corpus. Filters: censors, " +
      "techniques, defenses, year_min, year_max, venue, core_only. " +
      "Returns ranked records (core papers first, then year desc).",
    inputSchema: {
      type: "object",
      properties: {
        query: { type: "string" },
        censors: { type: "array", items: { type: "string" } },
        techniques: { type: "array", items: { type: "string" } },
        defenses: { type: "array", items: { type: "string" } },
        year_min: { type: "integer" },
        year_max: { type: "integer" },
        venue: { type: "string" },
        core_only: { type: "boolean" },
        limit: { type: "integer", default: 20 },
      },
    },
  },
  {
    name: "get_paper",
    description:
      "Full record (incl. abstract, notes, references) for a single paper id. " +
      "Returns the paper plus any extracted findings tagged to it.",
    inputSchema: {
      type: "object",
      required: ["id"],
      properties: { id: { type: "string" } },
    },
  },
  {
    name: "list_taxonomy",
    description:
      "Returns the controlled vocabulary (censors, techniques, defenses, " +
      "evaluation_methods, visibility_levels). Use this first in a session " +
      "to know the canonical IDs for filtering.",
    inputSchema: { type: "object", properties: {} },
  },
  {
    name: "find_related",
    description:
      "Papers that share tags with a given paper id. mode = 'same_technique' " +
      "(default), 'same_censor', or 'same_defense'.",
    inputSchema: {
      type: "object",
      required: ["id"],
      properties: {
        id: { type: "string" },
        mode: {
          type: "string",
          enum: ["same_technique", "same_censor", "same_defense"],
        },
        limit: { type: "integer", default: 10 },
      },
    },
  },
  {
    name: "synthesize",
    description:
      "Answer a research question by retrieving every relevant extracted finding " +
      "across all papers, attaching paper-level citation metadata, and grouping by " +
      "technique / censor / year. The caller (an LLM) writes the synthesized answer " +
      "from this material — every claim should cite (paper_id, §section). Use this " +
      "when the user asks 'what does the literature say about X', 'what's known about Y', " +
      "or wants a defense recommendation backed by citations. Prefer over search_papers " +
      "when the question is about a phenomenon rather than a specific paper.",
    inputSchema: {
      type: "object",
      required: ["question"],
      properties: {
        question: {
          type: "string",
          description:
            "The research question. Examples: 'What does the literature say about " +
            "Iran SNI-based blocking?', 'What's the evidence on GFW active probing?'",
        },
        censors: { type: "array", items: { type: "string" } },
        techniques: { type: "array", items: { type: "string" } },
        defenses: { type: "array", items: { type: "string" } },
        limit: { type: "integer", default: 30 },
      },
    },
  },
];

function lower(s: unknown): string {
  return typeof s === "string" ? s.toLowerCase() : "";
}

// stopwords stripped from the AND-match haystack so natural-language
// questions ("what does the literature say about active probing")
// don't collapse to zero matches because none of the framing words
// appear in finding summaries.
const STOPWORDS: ReadonlySet<string> = new Set([
  "a", "an", "the", "and", "or", "but", "of", "in", "on", "at", "to",
  "from", "for", "with", "by", "as", "into", "onto", "upon", "out",
  "over", "under", "than", "so", "if", "then",
  "is", "are", "was", "were", "be", "been", "being", "am",
  "have", "has", "had", "having",
  "do", "does", "did", "doing", "done",
  "i", "me", "my", "we", "us", "our",
  "you", "your", "he", "him", "his", "she", "her",
  "they", "them", "their", "it", "its",
  "what", "which", "who", "whom", "whose",
  "how", "when", "where", "why",
  "this", "that", "these", "those", "there", "here",
  "any", "some", "all",
  "can", "could", "will", "would", "should", "may", "might", "must",
  "say", "says", "said", "tell", "tells", "told",
  "about", "literature", "research", "papers", "paper",
  "finding", "findings", "show", "shows", "showed",
  "know", "known", "knows", "think",
  "please", "explain", "describe", "describes",
  "summarize", "summarise", "summary",
  "give", "gives", "list", "lists",
]);

function tokenize(s: string): string[] {
  return s
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter((t) => t && !STOPWORDS.has(t));
}

function textMatch(p: Paper, q: string): boolean {
  if (!q) return true;
  const haystack = [
    p.title,
    p.abstract || "",
    p.notes || "",
    p.id,
    (p.authors || []).join(" "),
    FINDINGS_TEXT_BY_PAPER.get(p.id) || "",
  ]
    .join(" ")
    .toLowerCase();
  for (const t of tokenize(q)) {
    if (!haystack.includes(t)) return false;
  }
  return true;
}

function anyOverlap(a: string[] | undefined, b: string[] | undefined): boolean {
  if (!a || !b || a.length === 0 || b.length === 0) return false;
  const set = new Set(a);
  for (const x of b) if (set.has(x)) return true;
  return false;
}

interface SearchArgs {
  query?: string;
  censors?: string[];
  techniques?: string[];
  defenses?: string[];
  year_min?: number;
  year_max?: number;
  venue?: string;
  core_only?: boolean;
  limit?: number;
}

function searchPapers(args: SearchArgs): Paper[] {
  const limit = Math.max(1, Math.min(100, args.limit ?? 20));
  const out: Paper[] = [];
  for (const p of CORPUS.papers) {
    if (args.core_only && !p.core) continue;
    if (args.year_min && p.year < args.year_min) continue;
    if (args.year_max && p.year > args.year_max) continue;
    if (args.venue && lower(p.venue).indexOf(lower(args.venue)) < 0) continue;
    if (args.censors && args.censors.length > 0 && !anyOverlap(args.censors, p.censors)) continue;
    if (args.techniques && args.techniques.length > 0 && !anyOverlap(args.techniques, p.techniques)) continue;
    if (args.defenses && args.defenses.length > 0) {
      const ds = [
        ...(p.defenses_discussed || []),
        ...(p.defenses_evaluated_against || []),
      ];
      if (!anyOverlap(args.defenses, ds)) continue;
    }
    if (args.query && !textMatch(p, args.query)) continue;
    out.push(p);
  }
  out.sort((a, b) => {
    if (!!a.core !== !!b.core) return a.core ? -1 : 1;
    return b.year - a.year;
  });
  return out.slice(0, limit);
}

function getPaper(id: string): { paper: Paper; findings: Finding[] } | null {
  const p = BY_ID.get(id);
  if (!p) return null;
  const findings = CORPUS.findings.filter((f) => f.paper === id);
  return { paper: p, findings };
}

function findRelated(id: string, mode: string, limit: number): Paper[] {
  const p = BY_ID.get(id);
  if (!p) return [];
  let key: string[];
  switch (mode) {
    case "same_censor":
      key = p.censors || [];
      break;
    case "same_defense":
      key = [...(p.defenses_discussed || []), ...(p.defenses_evaluated_against || [])];
      break;
    case "same_technique":
    default:
      key = p.techniques || [];
      break;
  }
  if (key.length === 0) return [];
  const scored: { p: Paper; s: number }[] = [];
  for (const q of CORPUS.papers) {
    if (q.id === id) continue;
    let qKey: string[];
    switch (mode) {
      case "same_censor":
        qKey = q.censors || [];
        break;
      case "same_defense":
        qKey = [...(q.defenses_discussed || []), ...(q.defenses_evaluated_against || [])];
        break;
      default:
        qKey = q.techniques || [];
        break;
    }
    let s = 0;
    for (const k of key) if (qKey.includes(k)) s++;
    if (s > 0) scored.push({ p: q, s });
  }
  scored.sort((a, b) => b.s - a.s || b.p.year - a.p.year);
  return scored.slice(0, Math.max(1, Math.min(50, limit))).map((x) => x.p);
}

interface SynthesizeArgs {
  question: string;
  censors?: string[];
  techniques?: string[];
  defenses?: string[];
  limit?: number;
}

function synthesize(args: SynthesizeArgs): unknown {
  const limit = Math.max(1, Math.min(100, args.limit ?? 30));
  const tokens = tokenize(args.question || "");
  const matched: Array<
    Finding & {
      paper_title: string;
      paper_authors?: string[];
      paper_year: number;
      paper_venue?: string;
      paper_url?: string;
    }
  > = [];
  const matchedPaperIDs = new Set<string>();

  for (const f of CORPUS.findings) {
    const p = BY_ID.get(f.paper);
    if (!p) continue;
    if (
      args.censors && args.censors.length > 0 &&
      !anyOverlap(args.censors, f.censors) && !anyOverlap(args.censors, p.censors)
    ) continue;
    if (
      args.techniques && args.techniques.length > 0 &&
      !anyOverlap(args.techniques, f.techniques) && !anyOverlap(args.techniques, p.techniques)
    ) continue;
    if (args.defenses && args.defenses.length > 0) {
      const paperDef = [
        ...(p.defenses_discussed || []),
        ...(p.defenses_evaluated_against || []),
      ];
      if (!anyOverlap(args.defenses, f.defenses) && !anyOverlap(args.defenses, paperDef)) continue;
    }
    if (tokens.length > 0) {
      const hay = [
        f.summary || "",
        (f.defense_implications || []).join(" "),
        f.section || "",
        p.title,
        p.abstract || "",
      ].join(" ").toLowerCase();
      let allHit = true;
      for (const t of tokens) {
        if (!hay.includes(t)) { allHit = false; break; }
      }
      if (!allHit) continue;
    }
    matched.push({
      ...f,
      paper_title: p.title,
      paper_authors: p.authors,
      paper_year: p.year,
      paper_venue: p.venue,
      paper_url: p.url,
    });
    matchedPaperIDs.add(f.paper);
  }

  matched.sort((a, b) => {
    if (a.paper_year !== b.paper_year) return b.paper_year - a.paper_year;
    return (a.id || "").localeCompare(b.id || "");
  });
  const truncated = matched.slice(0, limit);
  const truncatedPaperIDs = new Set(truncated.map((f) => f.paper));

  const byTechnique: Record<string, string[]> = {};
  const byCensor: Record<string, string[]> = {};
  const byYear: Record<string, string[]> = {};
  for (const f of truncated) {
    const fid = f.id || f.summary.slice(0, 32);
    for (const t of f.techniques || []) (byTechnique[t] ||= []).push(fid);
    for (const c of f.censors || []) (byCensor[c] ||= []).push(fid);
    const y = String(f.paper_year);
    (byYear[y] ||= []).push(fid);
  }

  // Papers that match the question lexically but have no findings extracted
  // — surfacing them tells the caller "this paper looks relevant but its
  // specific claims haven't been extracted; you may want to read it directly."
  const orphans: Paper[] = [];
  if (tokens.length > 0) {
    const papersWithFindings = new Set(CORPUS.findings.map((f) => f.paper));
    for (const p of CORPUS.papers) {
      if (truncatedPaperIDs.has(p.id) || papersWithFindings.has(p.id)) continue;
      const hay = [p.title, p.abstract || "", p.notes || ""].join(" ").toLowerCase();
      let allHit = true;
      for (const t of tokens) {
        if (!hay.includes(t)) { allHit = false; break; }
      }
      if (!allHit) continue;
      orphans.push(p);
      if (orphans.length >= 10) break;
    }
  }

  const papersWithAnyFindings = new Set(CORPUS.findings.map((f) => f.paper)).size;

  return {
    question: args.question,
    findings: truncated,
    grouped: { by_technique: byTechnique, by_censor: byCensor, by_year: byYear },
    papers_without_findings: orphans,
    counts: {
      matched_findings: truncated.length,
      matched_papers: truncatedPaperIDs.size,
      papers_without_findings: orphans.length,
      total_findings_in_corpus: CORPUS.findings.length,
      total_papers_in_corpus: CORPUS.papers.length,
      papers_with_any_findings: papersWithAnyFindings,
    },
    synthesis_hint: {
      role: "You are answering a research question using extracted findings from a censorship-circumvention research corpus.",
      format: "Produce a structured answer with three sections: (1) What's known — claims supported by multiple findings, (2) What's contested or uncertain — where findings diverge or are limited to one paper, (3) Open questions — gaps the literature hasn't addressed. Cite every claim inline as (paper_id, §section) using the IDs in the findings array.",
      caveats: "If counts.matched_findings is low (< 3), say so explicitly — the corpus may not have strong evidence on this question yet. If papers_without_findings is non-empty, mention that those papers may contain relevant claims that haven't been extracted yet.",
    },
    coverage: {
      corpus_size_papers: CORPUS.papers.length,
      papers_with_findings: papersWithAnyFindings,
      total_findings: CORPUS.findings.length,
    },
  };
}

interface RpcRequest {
  jsonrpc: "2.0";
  id?: string | number | null;
  method: string;
  params?: Record<string, unknown>;
}

interface RpcResponse {
  jsonrpc: "2.0";
  id: string | number | null;
  result?: unknown;
  error?: { code: number; message: string; data?: unknown };
}

function ok(id: RpcRequest["id"], result: unknown): RpcResponse {
  return { jsonrpc: "2.0", id: id ?? null, result };
}
function err(id: RpcRequest["id"], code: number, message: string): RpcResponse {
  return { jsonrpc: "2.0", id: id ?? null, error: { code, message } };
}

function callTool(name: string, args: Record<string, unknown>): unknown {
  switch (name) {
    case "search_papers": {
      const papers = searchPapers(args as SearchArgs);
      return {
        content: [{ type: "text", text: JSON.stringify({ count: papers.length, papers }, null, 2) }],
      };
    }
    case "get_paper": {
      const id = String(args.id || "");
      const got = getPaper(id);
      if (!got) {
        return {
          content: [{ type: "text", text: JSON.stringify({ error: `paper not found: ${id}` }) }],
          isError: true,
        };
      }
      return { content: [{ type: "text", text: JSON.stringify(got, null, 2) }] };
    }
    case "list_taxonomy": {
      return { content: [{ type: "text", text: JSON.stringify(CORPUS.taxonomy, null, 2) }] };
    }
    case "find_related": {
      const id = String(args.id || "");
      const mode = String(args.mode || "same_technique");
      const limit = Number(args.limit || 10);
      const papers = findRelated(id, mode, limit);
      return {
        content: [{ type: "text", text: JSON.stringify({ count: papers.length, papers }, null, 2) }],
      };
    }
    case "synthesize": {
      const bundle = synthesize(args as unknown as SynthesizeArgs);
      return { content: [{ type: "text", text: JSON.stringify(bundle, null, 2) }] };
    }
    default:
      throw new Error(`unknown tool: ${name}`);
  }
}

function handleRpc(req: RpcRequest): RpcResponse | null {
  switch (req.method) {
    case "initialize":
      return ok(req.id, {
        protocolVersion: PROTOCOL_VERSION,
        serverInfo: SERVER_INFO,
        capabilities: { tools: {} },
      });
    case "notifications/initialized":
    case "notifications/cancelled":
      // Notifications: no response.
      return null;
    case "tools/list":
      return ok(req.id, { tools: TOOLS });
    case "tools/call": {
      const params = req.params || {};
      const name = String(params.name || "");
      const args = (params.arguments as Record<string, unknown>) || {};
      try {
        return ok(req.id, callTool(name, args));
      } catch (e) {
        return err(req.id, -32000, (e as Error).message);
      }
    }
    case "ping":
      return ok(req.id, {});
    default:
      return err(req.id ?? null, -32601, `method not found: ${req.method}`);
  }
}

const CORS_HEADERS = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Methods": "POST, GET, OPTIONS",
  "Access-Control-Allow-Headers": "Content-Type, Mcp-Session-Id, Mcp-Protocol-Version",
};

export const onRequestOptions: PagesFunction = () =>
  new Response(null, { status: 204, headers: CORS_HEADERS });

export const onRequestGet: PagesFunction = () => {
  // Friendly landing for humans browsing https://corpus.lantern.io/mcp.
  const body = JSON.stringify(
    {
      service: "circumvention-corpus MCP",
      transport: "Streamable HTTP",
      protocol: PROTOCOL_VERSION,
      tools: TOOLS.map((t) => t.name),
      papers: CORPUS.papers.length,
      findings: CORPUS.findings.length,
      install: {
        "claude-code":
          "claude mcp add --transport http -s user circumvention-corpus https://corpus.lantern.io/mcp",
      },
    },
    null,
    2
  );
  return new Response(body, {
    status: 200,
    headers: { "Content-Type": "application/json", ...CORS_HEADERS },
  });
};

export const onRequestPost: PagesFunction = async ({ request }) => {
  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return jsonRpcResponse(err(null, -32700, "parse error"));
  }
  // Single request or batch.
  if (Array.isArray(body)) {
    const out = body
      .map((m) => handleRpc(m as RpcRequest))
      .filter((m): m is RpcResponse => m !== null);
    if (out.length === 0) return new Response(null, { status: 204, headers: CORS_HEADERS });
    return jsonRpcResponse(out);
  }
  const resp = handleRpc(body as RpcRequest);
  if (resp === null) return new Response(null, { status: 204, headers: CORS_HEADERS });
  return jsonRpcResponse(resp);
};

function jsonRpcResponse(payload: unknown): Response {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { "Content-Type": "application/json", ...CORS_HEADERS },
  });
}
