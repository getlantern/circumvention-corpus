package main

import (
	"fmt"
	"html/template"
	"strings"
)

// templates and CSS live inline in the binary so the site generator
// stays a single statically-linked Go binary that Cloudflare Pages can
// run directly. If the templates ever grow large enough to want files,
// switch to embed.FS — but at this size, inline is more legible.

var funcMap = template.FuncMap{
	"join": func(sep string, items []string) string {
		return strings.Join(items, sep)
	},
	"firstAuthor": func(authors []string) string {
		if len(authors) == 0 {
			return ""
		}
		// "Doe et al." for multi-author; just the name otherwise.
		if len(authors) == 1 {
			return authors[0]
		}
		// Try to extract a reasonable "et al" from the first author by
		// taking the last token.
		parts := strings.Fields(authors[0])
		if len(parts) > 0 {
			return parts[len(parts)-1] + " et al."
		}
		return authors[0]
	},
	"upper": strings.ToUpper,
	"yearString": func(y int) string {
		if y == 0 {
			return ""
		}
		return fmt.Sprintf("%d", y)
	},
}

// pages maps the logical page name (used by site.writeFile) to the
// page-specific body template. The layout has a {{block "main" .}}
// placeholder; per-page rendering clones the root + parses the
// page body, which overrides the empty block.
var pages = map[string]string{
	"index":        indexBody,
	"papers_index": papersIndexBody,
	"paper":        paperBody,
	"tag":          tagBody,
	"tag_index":    tagIndexBody,
	"taxonomy":     taxonomyBody,
	"contribute":   contributeBody,
	"use":          useBody,
}

// pageTemplates is a map[pageName]*template.Template, each one a clone
// of the shared layout with that page's "main" block parsed in.
type pageTemplates map[string]*template.Template

func mustTemplates() pageTemplates {
	root := template.Must(template.New("layout").Funcs(funcMap).Parse(layoutTmpl))
	out := pageTemplates{}
	for name, body := range pages {
		t := template.Must(root.Clone())
		template.Must(t.Parse(`{{define "main"}}` + body + `{{end}}`))
		out[name] = t
	}
	return out
}

const layoutTmpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<link rel="stylesheet" href="/style.css">
</head>
<body>
<header>
  <a class="brand" href="/"><span class="mono">circumvention-corpus</span></a>
  <nav>
    <a href="/papers/">papers</a>
    <a href="/censors/">censors</a>
    <a href="/techniques/">techniques</a>
    <a href="/defenses/">defenses</a>
    <a href="/taxonomy/">taxonomy</a>
    <a href="/use/">use</a>
    <a href="/contribute/">contribute</a>
    <a href="https://github.com/getlantern/circumvention-corpus" rel="external">github</a>
  </nav>
</header>
<main>{{block "main" .}}{{end}}</main>
<footer>
  <p>An LLM-callable, open-schema index of censorship-circumvention research. Maintained by the Lantern team and the broader circumvention community.</p>
  <p class="muted">Schema, taxonomy, and metadata: CC0 / public domain. Paper PDFs are not redistributed; each entry links to its canonical source.</p>
</footer>
</body>
</html>`

const indexBody = `
<section class="hero">
  <h1 class="mono">circumvention-corpus</h1>
  <p>A controlled-vocabulary, LLM-callable index of censorship-circumvention research. Each entry tags one paper against a shared taxonomy of censors, detection techniques, and defenses; an MCP server exposes the corpus to AI assistants.</p>
  <p>Complements <a href="https://github.com/net4people/bbs">net4people/bbs</a> (forum), <a href="https://gfw.report">gfw.report</a> (original research), <a href="https://censorbib.nymity.ch/">CensorBib</a> (bibliography), and <a href="https://ooni.org">OONI</a> (measurement) by adding the structured-metadata layer the others don't have.</p>
  <p><a href="/use/"><strong>How to use this →</strong></a> &nbsp; <a href="/contribute/">contribute →</a></p>
  <p class="counts">
    <a href="/papers/"><strong>{{.Counts.papers}}</strong> papers</a> ·
    <a href="/censors/"><strong>{{.Counts.censors}}</strong> censors</a> ·
    <a href="/techniques/"><strong>{{.Counts.techniques}}</strong> techniques</a> ·
    <a href="/defenses/"><strong>{{.Counts.defenses}}</strong> defenses</a>
  </p>
</section>

<section>
  <h2>Core papers</h2>
  <p class="muted">Hand-selected as load-bearing for protocol designers; team consensus.</p>
  <ul class="papers">
    {{range .Core}}
    <li>
      <a href="/papers/{{.ID}}/"><strong>{{.Title}}</strong></a>
      <span class="meta">{{firstAuthor .Authors}} · {{.Venue}} · {{yearString .Year}}</span>
    </li>
    {{end}}
  </ul>
</section>

<section>
  <h2>Recent additions</h2>
  <ul class="papers">
    {{range .Recent}}
    <li>
      <a href="/papers/{{.ID}}/"><strong>{{.Title}}</strong></a>
      <span class="meta">{{firstAuthor .Authors}} · {{.Venue}} · {{yearString .Year}}</span>
    </li>
    {{end}}
  </ul>
</section>
`

const papersIndexBody = `
<h1>All papers</h1>
<p class="muted">{{len .Papers}} entries, sorted by year (newest first).</p>
<ul class="papers full">
  {{range .Papers}}
  <li>
    <a href="/papers/{{.ID}}/"><strong>{{.Title}}</strong></a>
    <div class="meta">
      <span>{{join ", " .Authors}}</span>
      <span>·</span>
      <span>{{.Venue}}</span>
      <span>·</span>
      <span>{{yearString .Year}}</span>
      {{if .Core}}<span class="badge core">core</span>{{end}}
    </div>
    {{if .Censors}}<div class="tags">{{range .Censors}}<a class="tag censor" href="/censors/{{.}}/">{{.}}</a>{{end}}</div>{{end}}
  </li>
  {{end}}
</ul>
`

const paperBody = `
{{with .Paper}}
<article class="paper">
  <h1>{{.Title}}</h1>
  <p class="byline">
    {{join ", " .Authors}}{{if .Venue}} · <em>{{.Venue}}</em>{{end}}{{if .Year}} · {{.Year}}{{end}}
    {{if .Core}}<span class="badge core">core</span>{{end}}
  </p>

  {{if .URL}}<p><a href="{{.URL}}" rel="external">canonical link →</a>{{if .DOI}} · doi: <code>{{.DOI}}</code>{{end}}{{if .ArxivID}} · arxiv: <code>{{.ArxivID}}</code>{{end}}</p>{{end}}

  {{if .Abstract}}
  <h2>Abstract</h2>
  <div class="abstract">{{.Abstract}}</div>
  {{end}}

  {{if .Notes}}
  <h2>Team notes</h2>
  <div class="notes">{{.Notes}}</div>
  {{end}}

  <h2>Tags</h2>
  <dl class="tags-dl">
    <dt>Censors</dt><dd>{{range .Censors}}<a class="tag censor" href="/censors/{{.}}/">{{.}}</a>{{end}}</dd>
    <dt>Detection techniques</dt><dd>{{range .Techniques}}<a class="tag technique" href="/techniques/{{.}}/">{{.}}</a>{{end}}</dd>
    {{if .DefensesDiscussed}}<dt>Defenses discussed</dt><dd>{{range .DefensesDiscussed}}<a class="tag defense" href="/defenses/{{.}}/">{{.}}</a>{{end}}</dd>{{end}}
    {{if .DefensesEvaluatedAgainst}}<dt>Defenses evaluated against</dt><dd>{{range .DefensesEvaluatedAgainst}}<a class="tag defense" href="/defenses/{{.}}/">{{.}}</a>{{end}}</dd>{{end}}
    {{if .EvaluationMethods}}<dt>Evaluation methods</dt><dd>{{range .EvaluationMethods}}<span class="tag">{{.}}</span>{{end}}</dd>{{end}}
  </dl>
</article>
{{end}}

{{if .References}}
<section>
  <h2>References (in this corpus)</h2>
  <ul class="papers">
    {{range .References}}<li><a href="/papers/{{.ID}}/">{{.Title}}</a></li>{{end}}
  </ul>
</section>
{{end}}

{{if .Related}}
<section>
  <h2>Related papers</h2>
  <ul class="papers">
    {{range .Related}}
    <li>
      <a href="/papers/{{.ID}}/"><strong>{{.Title}}</strong></a>
      <span class="meta">{{firstAuthor .Authors}} · {{.Venue}} · {{yearString .Year}}</span>
    </li>
    {{end}}
  </ul>
</section>
{{end}}
`

const tagBody = `
<h1><span class="mono">{{.TagID}}</span> · {{.Entry.Name}}</h1>
{{if .Entry.Notes}}<p class="muted">{{.Entry.Notes}}</p>{{end}}
{{if .Entry.Synonyms}}<p class="muted">Synonyms: {{join ", " .Entry.Synonyms}}</p>{{end}}

<h2>{{len .Papers}} paper{{if ne (len .Papers) 1}}s{{end}}</h2>
<ul class="papers">
  {{range .Papers}}
  <li>
    <a href="/papers/{{.ID}}/"><strong>{{.Title}}</strong></a>
    <span class="meta">{{firstAuthor .Authors}} · {{.Venue}} · {{yearString .Year}}</span>
  </li>
  {{end}}
</ul>
`

const tagIndexBody = `
<h1>{{.Category}}</h1>
<p class="muted">Sorted by paper count.</p>
<ul class="tag-index">
  {{range .Rows}}
  <li>
    <a href="/{{$.Category}}/{{.ID}}/"><span class="mono">{{.ID}}</span></a>
    <span>{{.Entry.Name}}</span>
    <span class="muted">{{.Count}} paper{{if ne .Count 1}}s{{end}}</span>
  </li>
  {{end}}
</ul>
`

const taxonomyBody = `
<h1>Taxonomy</h1>
<p>The controlled vocabularies that all paper records tag against. Adding a term: <a href="https://github.com/getlantern/circumvention-corpus/blob/main/schema/taxonomy.yaml">edit <code>schema/taxonomy.yaml</code></a> and open a PR.</p>

<h2>Censors</h2>
<dl class="tax">
  {{range $id, $e := .Tax.Censors}}<dt><a href="/censors/{{$id}}/"><span class="mono">{{$id}}</span></a> {{$e.Name}}</dt><dd>{{if $e.Notes}}{{$e.Notes}}{{end}}{{if $e.Synonyms}}<br><span class="muted">synonyms: {{join ", " $e.Synonyms}}</span>{{end}}</dd>{{end}}
</dl>

<h2>Detection techniques</h2>
<dl class="tax">
  {{range $id, $e := .Tax.Techniques}}<dt><a href="/techniques/{{$id}}/"><span class="mono">{{$id}}</span></a> {{$e.Name}}</dt><dd>{{if $e.Notes}}{{$e.Notes}}{{end}}{{if $e.Synonyms}}<br><span class="muted">synonyms: {{join ", " $e.Synonyms}}</span>{{end}}</dd>{{end}}
</dl>

<h2>Defenses</h2>
<dl class="tax">
  {{range $id, $e := .Tax.Defenses}}<dt><a href="/defenses/{{$id}}/"><span class="mono">{{$id}}</span></a> {{$e.Name}}</dt><dd>{{if $e.Notes}}{{$e.Notes}}{{end}}{{if $e.Synonyms}}<br><span class="muted">synonyms: {{join ", " $e.Synonyms}}</span>{{end}}</dd>{{end}}
</dl>

<h2>Evaluation methods</h2>
<dl class="tax">
  {{range $id, $e := .Tax.EvaluationMethods}}<dt><span class="mono">{{$id}}</span> {{$e.Name}}</dt><dd>{{if $e.Notes}}{{$e.Notes}}{{end}}</dd>{{end}}
</dl>

<h2>Visibility levels</h2>
<dl class="tax">
  {{range $id, $e := .Tax.VisibilityLevels}}<dt><span class="mono">{{$id}}</span> {{$e.Name}}</dt><dd>{{if $e.Notes}}{{$e.Notes}}{{end}}</dd>{{end}}
</dl>
`

const useBody = `
<h1>Use the corpus</h1>

<p>The corpus is designed to be useful in several ways, sorted from
least to most setup. Pick whichever fits your workflow.</p>

<h2>1. Browse this site</h2>
<p>The simplest mode. Every paper has a stable URL: <code>/papers/&lt;id&gt;/</code>. Tag indexes (<a href="/censors/">censors</a>, <a href="/techniques/">techniques</a>, <a href="/defenses/">defenses</a>) let you walk the field by axis. The whole site rebuilds from the YAML on every push to <code>main</code>; whatever you see here matches the source repo.</p>

<h2>2. Read the YAML directly</h2>
<p>Every paper is a small YAML file in <a href="https://github.com/getlantern/circumvention-corpus/tree/main/corpus/papers">corpus/papers/</a>. The <a href="https://github.com/getlantern/circumvention-corpus/blob/main/schema/paper.schema.json">JSON schema</a> documents every field. The <a href="/taxonomy/">taxonomy</a> documents the controlled-vocabulary IDs that tag fields use. If you're building your own tooling on top of the corpus, this is the most boring, most stable interface — clone the repo, walk the directory.</p>

<pre><code>git clone https://github.com/getlantern/circumvention-corpus
cd circumvention-corpus
ls corpus/papers/                       # one YAML per paper
yq '.censors' corpus/papers/2023-wu-fully-encrypted-detect.yaml
</code></pre>

<h2>3. Run the MCP server (recommended)</h2>
<p>The most powerful mode: an LLM can query the corpus on demand and compose its results with whatever else it knows. The corpus ships its own <a href="https://github.com/getlantern/circumvention-corpus/tree/main/cmd/corpus-mcp">MCP server</a> in Go — single binary, zero non-stdlib runtime deps, reads the YAMLs at startup.</p>

<h3>Install</h3>
<pre><code>git clone https://github.com/getlantern/circumvention-corpus
cd circumvention-corpus
go build -o corpus-mcp ./cmd/corpus-mcp/

# Optional: put it on your PATH so MCP clients can launch it by name.
sudo mv corpus-mcp /usr/local/bin/
</code></pre>

<p>Or, if you only want to run the binary without managing a checkout:</p>
<pre><code>go install github.com/getlantern/circumvention-corpus/cmd/corpus-mcp@latest
</code></pre>

<h3>Register with Claude Code</h3>
<pre><code>claude mcp add -s user circumvention-corpus \
  /usr/local/bin/corpus-mcp -- --corpus $HOME/code/circumvention-corpus
</code></pre>
<p>Replace the <code>--corpus</code> path with wherever you cloned the repo. Verify with <code>claude mcp list</code>; it should show <code>✓ Connected</code>.</p>

<h3>Register with Claude Desktop</h3>
<p>Edit your Claude Desktop config:</p>
<ul>
  <li>macOS: <code>~/Library/Application Support/Claude/claude_desktop_config.json</code></li>
  <li>Windows: <code>%APPDATA%/Claude/claude_desktop_config.json</code></li>
</ul>
<pre><code>{
  "mcpServers": {
    "circumvention-corpus": {
      "command": "/usr/local/bin/corpus-mcp",
      "args": ["--corpus", "/Users/you/code/circumvention-corpus"]
    }
  }
}
</code></pre>
<p>Restart Claude Desktop; the server's tools become available in your conversations.</p>

<h3>Register with Cursor / VS Code Copilot / other MCP clients</h3>
<p>Any MCP-compliant client takes a stdio-launched binary. The shape:</p>
<pre><code>{
  "circumvention-corpus": {
    "command": "/usr/local/bin/corpus-mcp",
    "args": ["--corpus", "/path/to/circumvention-corpus"]
  }
}
</code></pre>
<p>For VS Code: drop the above into <code>.vscode/mcp.json</code> under a <code>"servers"</code> key. For Cursor: add it via <em>Settings → MCP → Add new MCP server</em>.</p>

<h2>What the MCP server exposes</h2>
<p>Four tools, designed to compose:</p>
<dl class="tax">
  <dt><span class="mono">search_papers</span></dt>
  <dd>Keyword + tag-filter search. Filters: <code>censors</code>, <code>techniques</code>, <code>defenses</code>, <code>year_min</code>, <code>year_max</code>, <code>venue</code>, <code>core_only</code>. Returns ranked records with abstract, tags, and team notes.</dd>
  <dt><span class="mono">get_paper</span></dt>
  <dd>Full record for a single paper id. Use after <code>search_papers</code> when the agent needs the full notes / references / metadata.</dd>
  <dt><span class="mono">list_taxonomy</span></dt>
  <dd>Returns the controlled vocabulary so the agent knows the canonical IDs to filter on. Especially useful as the first call in a session — gives the model the mental model of the field's structure.</dd>
  <dt><span class="mono">find_related</span></dt>
  <dd>Papers that share tags with a given paper. <code>mode</code> = <code>same_technique</code> (default), <code>same_censor</code>, or <code>same_defense</code>.</dd>
</dl>

<p>Example questions the MCP makes easy:</p>
<ul>
  <li><em>"Find every paper that evaluates a defense against the GFW's fully-encrypted-traffic detector."</em></li>
  <li><em>"What did anyone publish about Iran's censorship in 2024-2025?"</em></li>
  <li><em>"For my new protocol design: which papers should I read about active probing?"</em></li>
  <li><em>"Show me the citation neighborhood of <code>2023-wu-fully-encrypted-detect</code>."</em></li>
</ul>

<h2>4. Public MCP HTTPS endpoint</h2>
<p><em>Not yet live.</em> A read-only HTTPS endpoint at <code>corpus.lantern.io/mcp</code> is on the roadmap so other circumvention-tool teams can plug the corpus into their AI assistants without running anything locally. When it lands, point your MCP client at the HTTPS URL instead of a local binary.</p>

<h2>5. Build something on top</h2>
<p>The schema is CC0. The metadata is CC0. Build whatever you want with it — your own UI, a notification system that pings you when papers tagged with a specific technique appear, a sister index for a different region. The whole point of having a structured-metadata layer is that the data outlives whatever interface we put on top of it.</p>
`

const contributeBody = `
<h1>Contribute</h1>

<p>If you'd rather use the corpus than contribute to it, see <a href="/use/">use the corpus</a>.</p>

<h2>Add a paper</h2>
<ol>
  <li>Pick a stable id: <code>YYYY-firstauthor-shortslug</code> (lowercase, dashes).</li>
  <li>Create <code>corpus/papers/&lt;id&gt;.yaml</code> following <a href="https://github.com/getlantern/circumvention-corpus/blob/main/schema/paper.schema.json">the schema</a>.</li>
  <li>Tag against the controlled vocabulary in <a href="/taxonomy/">the taxonomy</a>. If a tag you need doesn't exist, add it to <code>schema/taxonomy.yaml</code> in the same PR.</li>
  <li>Set <code>visibility</code> honestly. If unsure, default to non-public; promoting later is easy, recalling a leak isn't.</li>
  <li>Write the <code>notes</code> field. The abstract is what the authors said; the notes are what your team thinks about it.</li>
  <li>Open a PR. CI runs the corpus integrity test (every tag must resolve, every reference must exist).</li>
</ol>

<h2>For private papers</h2>
<p>Don't put them in this repo. Use a separate private repo with the same schema (Lantern team: <code>circumvention-corpus-private</code>). The MCP server reads both data dirs locally; the public site you're reading right now serves only <code>visibility: public</code> records.</p>

<h2>Add a tag to the taxonomy</h2>
<p>Open a PR editing <a href="https://github.com/getlantern/circumvention-corpus/blob/main/schema/taxonomy.yaml"><code>schema/taxonomy.yaml</code></a>. New terms should have a definition and ideally a citation to a paper that uses the concept. Synonyms map alternate spellings to the canonical term.</p>

<h2>Extract findings from a paper</h2>
<p>The <code>findings/</code> directory holds extracted claims (one- to three-sentence statements like <em>"the GFW's classifier achieves 94% precision on Snowflake DTLS handshakes"</em>) tagged against the same vocabulary as papers. This is the highest-leverage curation work — it's what makes the corpus answer questions like <em>"what did anyone find about technique X"</em> without re-reading every paper.</p>
<p>An LLM (Claude/GPT/etc.) can propose findings if you feed it a paper; commit them only after a human review.</p>
`

const styleCSS = `
:root {
  --bg: #fafaf7;
  --fg: #1a1a1a;
  --muted: #666;
  --accent: #b14d29;
  --rule: #e3e3dc;
  --code-bg: #efeee9;
  --tag-bg: #ece9df;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #161614;
    --fg: #e9e7e0;
    --muted: #999;
    --accent: #e6814b;
    --rule: #2a2926;
    --code-bg: #25241f;
    --tag-bg: #2a2925;
  }
}
* { box-sizing: border-box; }
html { font-size: 16px; }
body {
  margin: 0;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
  font-size: 1rem;
  line-height: 1.55;
  background: var(--bg);
  color: var(--fg);
}
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem 1.5rem;
  border-bottom: 1px solid var(--rule);
  max-width: 70rem;
  margin: 0 auto;
}
.brand { font-weight: 600; text-decoration: none; color: var(--fg); }
nav { display: flex; gap: 1rem; flex-wrap: wrap; }
nav a { text-decoration: none; color: var(--fg); border-bottom: 1px solid transparent; padding-bottom: 1px; }
nav a:hover { border-bottom-color: var(--accent); }
main {
  max-width: 70rem;
  margin: 0 auto;
  padding: 2rem 1.5rem 4rem;
}
footer {
  max-width: 70rem;
  margin: 0 auto;
  padding: 2rem 1.5rem;
  border-top: 1px solid var(--rule);
  color: var(--muted);
  font-size: 0.9rem;
}
h1 { font-size: 1.75rem; margin: 0 0 0.5rem; }
h2 { font-size: 1.25rem; margin: 2rem 0 0.5rem; border-bottom: 1px solid var(--rule); padding-bottom: 0.25rem; }
.muted { color: var(--muted); }
a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }
code { background: var(--code-bg); padding: 0 0.25rem; border-radius: 3px; font-size: 0.9em; }
pre { background: var(--code-bg); padding: 1rem; border-radius: 5px; overflow-x: auto; }
pre code { background: none; padding: 0; }
.hero { margin-bottom: 2rem; }
.counts { font-size: 1.05rem; }
.papers { list-style: none; padding: 0; }
.papers li { margin-bottom: 0.75rem; }
.papers .meta { display: block; color: var(--muted); font-size: 0.9rem; }
.papers.full li { margin-bottom: 1.25rem; padding-bottom: 1.25rem; border-bottom: 1px solid var(--rule); }
.papers.full li:last-child { border-bottom: none; }
.byline { color: var(--muted); }
.abstract { white-space: pre-wrap; }
.notes { white-space: pre-wrap; padding: 1rem; background: var(--code-bg); border-left: 3px solid var(--accent); border-radius: 3px; }
.tags-dl dt { font-weight: 600; margin-top: 0.5rem; color: var(--muted); font-size: 0.9rem; }
.tags-dl dd { margin: 0 0 0.25rem; padding: 0; }
.tag { display: inline-block; padding: 1px 0.5rem; margin: 0.15rem 0.25rem 0.15rem 0; background: var(--tag-bg); border-radius: 3px; font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 0.85em; text-decoration: none; color: var(--fg); }
.tag:hover { background: var(--accent); color: white; }
.tag.censor { border-left: 3px solid #c4452f; }
.tag.technique { border-left: 3px solid #8b6914; }
.tag.defense { border-left: 3px solid #2c6e49; }
.tags { margin-top: 0.25rem; }
.badge { display: inline-block; font-size: 0.75rem; padding: 0 0.4rem; border-radius: 3px; background: var(--accent); color: white; vertical-align: middle; margin-left: 0.4rem; }
.tag-index { list-style: none; padding: 0; display: grid; grid-template-columns: max-content 1fr max-content; row-gap: 0.4rem; column-gap: 1rem; }
.tag-index li { display: contents; }
.tax dt { font-weight: 600; margin-top: 0.75rem; }
.tax dd { margin: 0 0 0.25rem; color: var(--muted); }
`
