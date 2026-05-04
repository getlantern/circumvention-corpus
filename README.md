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
cmd/corpus-mcp/      The MCP server.
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

## Running the MCP server

```bash
go build -o corpus-mcp ./cmd/corpus-mcp/
./corpus-mcp --corpus /path/to/circumvention-corpus
```

Register with Claude Code at user scope:

```bash
claude mcp add -s user circumvention-corpus \
  /usr/local/bin/corpus-mcp -- --corpus $HOME/go/src/github.com/getlantern/circumvention-corpus
```

For a local team instance that also reads the private repo, point at a
parent directory containing both, or run two server instances and let
the agent compose results.

For the public read-only endpoint:

```bash
./corpus-mcp --corpus /path/to/circumvention-corpus --public-only
```

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
