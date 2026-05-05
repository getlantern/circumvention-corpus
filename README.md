# circumvention-corpus

A structured, LLM-callable corpus of censorship-circumvention research.

This is a research index, not a paper hosting site. Each entry is a YAML
record with the paper's core metadata, controlled-vocabulary tags
(censor / detection technique / defense), team notes, and (optionally)
extracted findings. The whole thing is exposed through an MCP server so
an LLM can answer questions like "what did anyone find about active
probing in 2024?" or "find every paper that evaluates a defense against
the GFW's fully-encrypted-traffic detector" without anyone re-reading
the field.

## Why this exists

The field already has wonderful resources: [net4people/bbs] is the
discussion forum, [gfw.report] publishes original research, [CensorBib]
is a maintained bibliography, [OONI] runs network measurement. None of
them are LLM-callable, none of them have a consistent structured
metadata schema, and none of them let a Claude/GPT-shaped agent compose
a corpus query with an internal-data query in the same conversation.

This corpus adds that one missing layer: a controlled-vocabulary metadata
schema over the field's existing literature, with a tiny MCP server
that lets any AI assistant query it.

[net4people/bbs]: https://github.com/net4people/bbs
[gfw.report]: https://gfw.report
[CensorBib]: https://censorbib.nymity.ch/
[OONI]: https://ooni.org

## How it's organized

```
schema/              JSON Schemas + the YAML taxonomy. The durable artifact.
  paper.schema.json
  finding.schema.json
  taxonomy.yaml      Controlled vocabulary: censors, techniques, defenses.
corpus/
  papers/            One YAML per paper.
  findings/          (Optional) extracted findings, one YAML per paper.
  pdfs/              (Optional) local PDF cache.
cmd/corpus-mcp/      Local stdio MCP server (Go) — offline / privacy fallback.
cmd/corpus-bundle/   Bundles YAMLs into JSON for the Worker to inline.
cmd/corpus-site/     Static site generator (Go html/template).
functions/mcp/       Hosted MCP server (Cloudflare Pages Function, TS).
```

## Visibility model

Every paper has a `visibility` field. The schema has four levels:

| Level | What it means | Where it can be served |
| --- | --- | --- |
| `public` | Published openly, fine to share with anyone | Public MCP endpoint + local |
| `community` | Shared in confidence among circumvention-tool developers | Local only |
| `internal` | Lantern team only — partner draft, NDA'd doc, leaked material | Local only |
| `embargoed` | Will become public on a known date (`embargo_until` required) | Local only until the date |

**The repo split:**

- `circumvention-corpus` (this repo) — public. Holds only `visibility:
  public` papers.
- `circumvention-corpus-private` (separate, private) — Lantern team only.
  Holds the `community`, `internal`, and `embargoed` papers, with the
  same schema.

The MCP server takes one or more corpus directories. The local
Lantern-team Claude Code instance points at both; the public-facing
endpoint runs with `--public-only` and reads only this repo. That's a
hard data-path separation, not a runtime filter — an authorization bug
in the public endpoint can't accidentally serve a private record because
the private records aren't on disk.

When a private paper uses an LLM-derived finding, the redistribution
constraints are propagated: the `redistribution_terms` field is
included in `get_paper` results so the calling agent knows what it
can and can't do with the citation.

## Browsable site

A static site rendered from the same YAMLs lives at
**[corpus.lantern.io](https://corpus.lantern.io)**, auto-deployed from
`main` via the workflow in `.github/workflows/deploy.yaml`. Build it
locally:

```bash
make site            # renders to ./dist/
python3 -m http.server -d dist 8080
```

The site uses `cmd/corpus-site/`, which reuses the same YAML loading
the MCP server uses. There's no JS framework, no `node_modules`, just
Go's `html/template`. About 0.1 seconds to render the whole corpus.

## Using the MCP server

### Hosted (recommended)

The corpus runs as a Cloudflare Pages Function at
`https://corpus.lantern.io/mcp`. Zero install, no toolchain, no version
drift — auto-deploys on every push to `main` so connected clients always
see the latest committed state.

```bash
claude mcp add --transport http -s user circumvention-corpus \
  https://corpus.lantern.io/mcp
```

Or in any MCP client's config:

```json
{
  "mcpServers": {
    "circumvention-corpus": {
      "url": "https://corpus.lantern.io/mcp",
      "transport": "http"
    }
  }
}
```

### Self-hosted (offline / privacy)

For users behind censorship that blocks Cloudflare, or anyone who'd
rather not send queries off-machine. The Go stdio server in
`cmd/corpus-mcp/` reads YAMLs directly from a local clone:

```bash
go install github.com/getlantern/circumvention-corpus/cmd/corpus-mcp@latest
git clone https://github.com/getlantern/circumvention-corpus ~/code/circumvention-corpus

claude mcp add -s user circumvention-corpus \
  $(go env GOPATH)/bin/corpus-mcp -- --corpus $HOME/code/circumvention-corpus
```

For a local team instance that also reads the private companion repo,
point at a parent directory containing both, or run two server
instances and let the agent compose results. Use `--public-only` to
mirror the visibility clamp the hosted endpoint applies.

### Architecture

The hosted MCP is a Cloudflare Pages Function (`functions/mcp/index.ts`)
implementing MCP's Streamable HTTP transport directly — POST JSON-RPC,
get JSON back. The corpus is bundled into the Worker at deploy time by
`cmd/corpus-bundle/` (Go), which reads every YAML and emits
`functions/_data/corpus.json`; esbuild inlines that JSON into the
Worker bundle, so there's no R2 read or runtime fetch.

Push to `main` → CI runs the bundler → Wrangler deploys the static site
+ the Function together → live in ~60 s for everyone.

## Tools the MCP exposes

- `search_papers` — keyword + tag-filter search. Returns ranked records.
- `get_paper` — fetch a single record by id.
- `list_taxonomy` — return the controlled vocabulary so the agent knows
  what tag IDs to filter on.
- `find_related` — papers that share censor / technique / defense tags.

## Adding a paper

1. Pick a stable id: `YYYY-firstauthor-shortslug` (lowercase, dashes).
2. Create `corpus/papers/<id>.yaml` from the schema.
3. Tag against the controlled vocabulary in `schema/taxonomy.yaml`. If
   you need a tag that doesn't exist, add it to the taxonomy in the
   same PR.
4. Set `visibility` honestly. If unsure, default to non-public; promoting
   later is easy, recalling a leak isn't.
5. Write the `notes` field. This is where the team's actual judgment
   lives — the abstract is what the authors said, the notes are what we
   think about it.

For private papers: open the PR against `circumvention-corpus-private`
instead. Same schema, same review process.

## Curation philosophy

**Curation beats volume.** A hand-selected 200-paper corpus the team
trusts is more useful than a 50,000-paper auto-crawl. We do run crawlers
(arXiv, USENIX, NDSS, FOCI proceedings, net4people watcher), but they
*propose* additions to a triage queue rather than auto-ingesting. A
human always decides what enters the corpus.

The bar is roughly: would I expect a Lantern protocol designer to be
slowed down if they hadn't read this? If yes, it's a candidate.
"Comprehensive coverage of the field" is a non-goal.

## Contributing

Most welcome:

- New papers (especially recent measurement / threat-model work)
- Better tags on existing papers
- Findings extracted from papers (one-to-three-sentence claims with
  paper id + section reference)
- Taxonomy additions when the existing terms don't fit

Issues are open. PRs are open. The maintainers (Lantern team for now)
will get involved as needed; ideally the schema is uncontroversial
enough that most PRs are routine merges.

## License

Schema, taxonomy, and corpus metadata: CC0 / public domain.
Paper PDFs (when stored locally): NOT redistributed. Each paper YAML
points at the canonical URL; downloads are the user's responsibility.
