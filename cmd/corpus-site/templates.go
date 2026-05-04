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
<meta name="color-scheme" content="light dark">
<title>{{.Title}}</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Fraunces:opsz,wght,SOFT@9..144,400;9..144,500;9..144,700;9..144,900&family=Inter:wght@400;500;600&family=JetBrains+Mono:wght@400;600&display=swap" rel="stylesheet">
<link rel="stylesheet" href="/style.css">
<script src="/search.js" defer></script>
</head>
<body>
<header class="site-header">
  <div class="wrap">
    <a class="brand" href="/">
      <span class="brand-mark">▤</span>
      <span class="brand-name">circumvention-corpus</span>
    </a>
    <div class="search-wrap">
      <input id="search" type="search" placeholder="Search papers… (e.g. active probing, GFW, Iran 2025)" autocomplete="off" spellcheck="false">
      <kbd class="search-kbd">/</kbd>
      <div id="search-results" hidden></div>
    </div>
    <nav>
      <a href="/papers/">papers</a>
      <a href="/censors/">censors</a>
      <a href="/techniques/">techniques</a>
      <a href="/defenses/">defenses</a>
      <a href="/taxonomy/">taxonomy</a>
      <a href="/use/">use</a>
      <a href="/contribute/">contribute</a>
      <a class="external" href="https://github.com/getlantern/circumvention-corpus" rel="external">github →</a>
    </nav>
  </div>
</header>
<main class="wrap">{{block "main" .}}{{end}}</main>
<footer class="site-footer">
  <div class="wrap">
    <div class="foot-grid">
      <div>
        <div class="foot-title">circumvention-corpus</div>
        <p>A controlled-vocabulary, LLM-callable index of censorship-circumvention research.</p>
      </div>
      <div>
        <div class="foot-title">Browse</div>
        <ul>
          <li><a href="/papers/">All papers</a></li>
          <li><a href="/censors/">By censor</a></li>
          <li><a href="/techniques/">By technique</a></li>
          <li><a href="/defenses/">By defense</a></li>
        </ul>
      </div>
      <div>
        <div class="foot-title">Use</div>
        <ul>
          <li><a href="/use/">MCP server install</a></li>
          <li><a href="/contribute/">Contribute a paper</a></li>
          <li><a href="/taxonomy/">Taxonomy reference</a></li>
          <li><a href="https://github.com/getlantern/circumvention-corpus" rel="external">Source on GitHub</a></li>
        </ul>
      </div>
      <div>
        <div class="foot-title">Companion projects</div>
        <ul>
          <li><a href="https://github.com/net4people/bbs" rel="external">net4people/bbs</a> — forum</li>
          <li><a href="https://gfw.report" rel="external">gfw.report</a> — original research</li>
          <li><a href="https://censorbib.nymity.ch/" rel="external">CensorBib</a> — bibliography</li>
          <li><a href="https://ooni.org" rel="external">OONI</a> — measurement</li>
        </ul>
      </div>
    </div>
    <p class="legal">Schema, taxonomy, and metadata: CC0 / public domain. Paper PDFs are not redistributed; each entry links to its canonical source. Maintained by the <a href="https://lantern.io" rel="external">Lantern</a> team and the broader circumvention community.</p>
  </div>
</footer>
</body>
</html>`

const indexBody = `
<section class="hero">
  <p class="eyebrow">CIRCUMVENTION RESEARCH · STRUCTURED · LLM-CALLABLE</p>
  <h1 class="display">The field's literature, <em>indexed</em>.</h1>
  <p class="lede">A controlled-vocabulary corpus of censorship-circumvention research. Each paper is tagged against a shared taxonomy of <a href="/censors/">censors</a>, <a href="/techniques/">detection techniques</a>, and <a href="/defenses/">defenses</a>. An MCP server exposes it to any AI assistant.</p>
  <div class="cta">
    <a class="btn primary" href="/use/">Install the MCP server →</a>
    <a class="btn ghost" href="/papers/">Browse {{.Counts.papers}} papers</a>
  </div>
  <dl class="counts-grid">
    <div><dt>papers</dt><dd>{{.Counts.papers}}</dd></div>
    <div><dt>censors</dt><dd>{{.Counts.censors}}</dd></div>
    <div><dt>techniques</dt><dd>{{.Counts.techniques}}</dd></div>
    <div><dt>defenses</dt><dd>{{.Counts.defenses}}</dd></div>
  </dl>
</section>

<section class="why">
  <p class="section-mark">§ I — WHY THIS EXISTS</p>
  <div class="two-col">
    <div>
      <h2 class="display-sm">A layer the field doesn't have yet.</h2>
      <p>The censorship-circumvention community has wonderful resources: <a href="https://github.com/net4people/bbs" rel="external">net4people/bbs</a> for discussion, <a href="https://gfw.report" rel="external">gfw.report</a> for original research, <a href="https://censorbib.nymity.ch/" rel="external">CensorBib</a> as a maintained bibliography, <a href="https://ooni.org" rel="external">OONI</a> for measurement.</p>
      <p>None of them are LLM-callable. None of them have a consistent structured-metadata schema. None of them let an AI assistant compose a corpus query with operational data in the same conversation.</p>
      <p>This corpus adds that one missing layer.</p>
    </div>
    <div class="aside">
      <p class="aside-label">The thing that compounds</p>
      <p>The schema and the controlled vocabulary outlive whatever model you read it through. Frontier models change every six months. The taxonomy of censors / techniques / defenses doesn't.</p>
    </div>
  </div>
</section>

<section class="core">
  <p class="section-mark">§ II — CORE PAPERS</p>
  <h2 class="display-sm">Hand-selected as load-bearing.</h2>
  <p class="muted">If a Lantern protocol designer hadn't read these, the team would expect them to be slowed down. Team consensus marks them as <code>core: true</code>; everyone using the corpus sees them surfaced first.</p>
  <ul class="paper-cards">
    {{range .Core}}
    <li class="paper-card">
      <a href="/papers/{{.ID}}/" class="card-link">
        <div class="card-id mono">{{.ID}}</div>
        <h3>{{.Title}}</h3>
        <div class="card-meta">{{firstAuthor .Authors}} · <em>{{.Venue}}</em> · {{yearString .Year}}</div>
        <div class="card-tags">{{range .Censors}}<span class="tag censor">{{.}}</span>{{end}}{{range .Techniques}}<span class="tag technique">{{.}}</span>{{end}}</div>
      </a>
    </li>
    {{end}}
  </ul>
</section>

<section class="recent">
  <p class="section-mark">§ III — RECENT ADDITIONS</p>
  <ul class="paper-list">
    {{range .Recent}}
    <li>
      <a href="/papers/{{.ID}}/">
        <span class="row-id mono">{{.ID}}</span>
        <span class="row-title">{{.Title}}</span>
        <span class="row-meta">{{.Venue}} · {{yearString .Year}}</span>
      </a>
    </li>
    {{end}}
  </ul>
</section>

<section class="cta-bottom">
  <h2 class="display-sm">Plug it into your assistant.</h2>
  <p class="lede">One install. Your AI gains <code>search_papers</code>, <code>get_paper</code>, <code>list_taxonomy</code>, and <code>find_related</code> over the corpus.</p>
  <a class="btn primary" href="/use/">How to install →</a>
</section>
`

const papersIndexBody = `
<p class="eyebrow">{{len .Papers}} ENTRIES · NEWEST FIRST</p>
<h1 class="display-sm">All papers</h1>
<ul class="paper-cards">
  {{range .Papers}}
  <li class="paper-card">
    <a href="/papers/{{.ID}}/" class="card-link">
      <div class="card-id mono">{{.ID}}{{if .Core}} · core{{end}}</div>
      <h3>{{.Title}}</h3>
      <div class="card-meta">{{firstAuthor .Authors}} · <em>{{.Venue}}</em> · {{yearString .Year}}</div>
      <div class="card-tags">
        {{range .Censors}}<span class="tag censor">{{.}}</span>{{end}}
        {{range .Techniques}}<span class="tag technique">{{.}}</span>{{end}}
      </div>
    </a>
  </li>
  {{end}}
</ul>
`

const paperBody = `
{{with .Paper}}
<article class="paper">
  <p class="paper-id mono">{{.ID}}</p>
  <h1>{{.Title}}{{if .Core}}<span class="badge core">core</span>{{end}}</h1>
  <p class="byline">
    {{join ", " .Authors}}{{if .Venue}} · <em>{{.Venue}}</em>{{end}}{{if .Year}} · {{.Year}}{{end}}
  </p>

  {{if .URL}}<p class="paper-links"><a href="{{.URL}}" rel="external">canonical link →</a>{{if .DOI}} · doi: <code>{{.DOI}}</code>{{end}}{{if .ArxivID}} · arxiv: <code>{{.ArxivID}}</code>{{end}}</p>{{end}}

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
    <dt>censors</dt><dd>{{range .Censors}}<a class="tag censor" href="/censors/{{.}}/">{{.}}</a>{{end}}</dd>
    <dt>techniques</dt><dd>{{range .Techniques}}<a class="tag technique" href="/techniques/{{.}}/">{{.}}</a>{{end}}</dd>
    {{if .DefensesDiscussed}}<dt>defenses</dt><dd>{{range .DefensesDiscussed}}<a class="tag defense" href="/defenses/{{.}}/">{{.}}</a>{{end}}</dd>{{end}}
    {{if .DefensesEvaluatedAgainst}}<dt>evaluated</dt><dd>{{range .DefensesEvaluatedAgainst}}<a class="tag defense" href="/defenses/{{.}}/">{{.}}</a>{{end}}</dd>{{end}}
    {{if .EvaluationMethods}}<dt>method</dt><dd>{{range .EvaluationMethods}}<span class="tag">{{.}}</span>{{end}}</dd>{{end}}
  </dl>
</article>
{{end}}

{{if .References}}
<section class="related-section">
  <p class="section-mark">REFERENCES IN THIS CORPUS</p>
  <ul class="paper-list">
    {{range .References}}
    <li><a href="/papers/{{.ID}}/">
      <span class="row-id mono">{{.ID}}</span>
      <span class="row-title">{{.Title}}</span>
      <span class="row-meta">{{.Venue}} · {{yearString .Year}}</span>
    </a></li>
    {{end}}
  </ul>
</section>
{{end}}

{{if .Related}}
<section class="related-section">
  <p class="section-mark">RELATED PAPERS</p>
  <ul class="paper-list">
    {{range .Related}}
    <li><a href="/papers/{{.ID}}/">
      <span class="row-id mono">{{.ID}}</span>
      <span class="row-title">{{.Title}}</span>
      <span class="row-meta">{{.Venue}} · {{yearString .Year}}</span>
    </a></li>
    {{end}}
  </ul>
</section>
{{end}}
`

const tagBody = `
<p class="eyebrow">{{upper .Category}}</p>
<h1 class="display-sm"><span class="mono tag-name">{{.TagID}}</span> &nbsp; {{.Entry.Name}}</h1>
{{if .Entry.Notes}}<p class="lede">{{.Entry.Notes}}</p>{{end}}
{{if .Entry.Synonyms}}<p class="muted"><strong>Synonyms:</strong> {{join ", " .Entry.Synonyms}}</p>{{end}}

<p class="section-mark">{{len .Papers}} PAPER{{if ne (len .Papers) 1}}S{{end}}</p>
<ul class="paper-list">
  {{range .Papers}}
  <li><a href="/papers/{{.ID}}/">
    <span class="row-id mono">{{.ID}}</span>
    <span class="row-title">{{.Title}}</span>
    <span class="row-meta">{{.Venue}} · {{yearString .Year}}</span>
  </a></li>
  {{end}}
</ul>
`

const tagIndexBody = `
<p class="eyebrow">CONTROLLED VOCABULARY · {{upper .Category}}</p>
<h1 class="display-sm">By {{.Category}}</h1>
<p class="lede muted">Sorted by paper count.</p>
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

// searchJS is the in-browser search client. Loaded on every page via
// /search.js. Fetches /search-index.json once on first focus, then
// runs in-memory keyword + tag matching. Vanilla JS, no framework, no
// build step. All result-row content is built via DOM APIs (no
// innerHTML on dynamic data) so XSS-via-paper-title isn't reachable.
//
// Ranking is deliberately simple: for each query token, score each
// paper by where the token appears (title 5x, authors 3x, tags 3x,
// notes 2x, abstract 1x). Title-prefix matches get an additional boost.
const searchJS = `(() => {
  const input = document.getElementById('search');
  const results = document.getElementById('search-results');
  if (!input || !results) return;

  let index = null;
  let loading = null;
  let activeIdx = -1;

  async function load() {
    if (index) return index;
    if (loading) return loading;
    loading = fetch('/search-index.json').then(r => r.json()).then(d => {
      index = d;
      return index;
    }).catch(() => { index = []; return index; });
    return loading;
  }

  function tokenize(s) {
    return (s || '').toLowerCase().split(/[^a-z0-9]+/).filter(Boolean);
  }

  function score(paper, qTokens) {
    let total = 0;
    const title = (paper.title || '').toLowerCase();
    const authors = (paper.authors || []).join(' ').toLowerCase();
    const tags = [...(paper.censors||[]), ...(paper.techniques||[]), ...(paper.defenses||[])].join(' ').toLowerCase();
    const notes = (paper.notes || '').toLowerCase();
    const abstract = (paper.abstract || '').toLowerCase();
    const id = (paper.id || '').toLowerCase();
    for (const t of qTokens) {
      if (!t) continue;
      let s = 0;
      if (title.includes(t)) s += 5;
      if (title.startsWith(t) || title.includes(' ' + t)) s += 3;
      if (authors.includes(t)) s += 3;
      if (tags.includes(t)) s += 3;
      if (id.includes(t)) s += 2;
      if (notes.includes(t)) s += 2;
      if (abstract.includes(t)) s += 1;
      if (s === 0) return 0;
      total += s;
    }
    if (paper.core) total += 1;
    return total;
  }

  // appendHighlighted writes 'text' into 'parent' as text nodes,
  // wrapping any case-insensitive occurrence of one of qTokens in a
  // <mark> element. Uses matchAll() iteration so all dynamic strings
  // go through createTextNode — there's no path for HTML injection
  // even from a maliciously-titled paper.
  function appendHighlighted(parent, text, qTokens) {
    if (!text) return;
    const meaningful = qTokens.filter(t => t && t.length >= 2);
    if (meaningful.length === 0) {
      parent.appendChild(document.createTextNode(text));
      return;
    }
    const escaped = meaningful.map(t => t.replace(/[.*+?^${}()|[\\]\\\\]/g, '\\\\$&')).join('|');
    const re = new RegExp('(' + escaped + ')', 'ig');
    let last = 0;
    for (const m of text.matchAll(re)) {
      if (m.index > last) parent.appendChild(document.createTextNode(text.slice(last, m.index)));
      const mark = document.createElement('mark');
      mark.textContent = m[0];
      parent.appendChild(mark);
      last = m.index + m[0].length;
    }
    if (last < text.length) parent.appendChild(document.createTextNode(text.slice(last)));
  }

  function clearChildren(el) {
    while (el.firstChild) el.removeChild(el.firstChild);
  }

  function tagSpan(text, kind) {
    const s = document.createElement('span');
    s.className = 'tag ' + kind;
    s.textContent = text;
    return s;
  }

  function buildRow(paper, qTokens) {
    const a = document.createElement('a');
    a.href = '/papers/' + encodeURIComponent(paper.id) + '/';
    const title = document.createElement('span');
    title.className = 'r-title';
    appendHighlighted(title, paper.title || '', qTokens);
    a.appendChild(title);
    const meta = document.createElement('span');
    meta.className = 'r-meta';
    const venue = paper.venue || '';
    const year = paper.year ? ' · ' + paper.year : '';
    meta.textContent = venue + year;
    a.appendChild(meta);
    const id = document.createElement('span');
    id.className = 'r-id';
    id.textContent = paper.id;
    a.appendChild(id);
    if ((paper.censors && paper.censors.length) || (paper.techniques && paper.techniques.length)) {
      const tags = document.createElement('span');
      tags.className = 'r-tags';
      for (const t of (paper.censors || [])) tags.appendChild(tagSpan(t, 'censor'));
      for (const t of (paper.techniques || [])) tags.appendChild(tagSpan(t, 'technique'));
      a.appendChild(tags);
    }
    return a;
  }

  function render(matches, qTokens, query) {
    clearChildren(results);
    if (!query) { results.hidden = true; activeIdx = -1; return; }
    if (matches.length === 0) {
      const empty = document.createElement('div');
      empty.className = 'empty';
      empty.appendChild(document.createTextNode('No matches for '));
      const em = document.createElement('em');
      em.textContent = query;
      empty.appendChild(em);
      empty.appendChild(document.createTextNode('.'));
      results.appendChild(empty);
      results.hidden = false;
      activeIdx = -1;
      return;
    }
    const summary = document.createElement('div');
    summary.className = 'summary';
    summary.textContent = matches.length + ' match' + (matches.length === 1 ? '' : 'es');
    results.appendChild(summary);
    for (const p of matches.slice(0, 30)) {
      results.appendChild(buildRow(p, qTokens));
    }
    results.hidden = false;
    activeIdx = -1;
  }

  async function update() {
    const query = input.value.trim();
    if (!query) { render([], [], ''); return; }
    await load();
    if (!index) return;
    const qTokens = tokenize(query);
    if (qTokens.length === 0) { render([], [], ''); return; }
    const scored = [];
    for (const p of index) {
      const s = score(p, qTokens);
      if (s > 0) scored.push({p, s});
    }
    scored.sort((a, b) => b.s - a.s || (b.p.year || 0) - (a.p.year || 0));
    render(scored.map(x => x.p), qTokens, query);
  }

  let timer = null;
  input.addEventListener('input', () => {
    clearTimeout(timer);
    timer = setTimeout(update, 80);
  });
  input.addEventListener('focus', load);

  // "/" anywhere focuses the search, like GitHub.
  document.addEventListener('keydown', e => {
    if (e.key === '/' && !['INPUT','TEXTAREA'].includes(document.activeElement.tagName)) {
      e.preventDefault();
      input.focus();
      input.select();
    }
    if (e.key === 'Escape' && document.activeElement === input) {
      input.value = '';
      results.hidden = true;
      input.blur();
    }
  });

  // Arrow-key navigation through results.
  input.addEventListener('keydown', e => {
    const items = results.querySelectorAll('a');
    if (!items.length) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      activeIdx = Math.min(items.length - 1, activeIdx + 1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      activeIdx = Math.max(0, activeIdx - 1);
    } else if (e.key === 'Enter') {
      if (activeIdx >= 0) {
        e.preventDefault();
        items[activeIdx].click();
      }
    } else {
      return;
    }
    items.forEach((it, i) => it.classList.toggle('active', i === activeIdx));
    items[activeIdx].scrollIntoView({block: 'nearest'});
  });

  // Click-outside dismiss.
  document.addEventListener('click', e => {
    if (!input.contains(e.target) && !results.contains(e.target)) {
      results.hidden = true;
    }
  });
})();
`

const styleCSS = `
/* circumvention-corpus — visual design.
 *
 * Palette: warm cream paper / deep ink. Single accent (deep teal) so
 * the colour story doesn't fight the typography. Category-coded tag
 * chips for censor / technique / defense — desaturated, never garish.
 *
 * Typography: Fraunces (a serif with optical sizing) for display +
 * paper titles, Inter for UI / nav / body, JetBrains Mono for IDs and
 * controlled-vocabulary chips. Loaded from Google Fonts.
 *
 * The aesthetic is "field manual" — academic but contemporary. Light
 * default because researchers read on bright screens and we're
 * cosplaying paper.
 */

:root {
  --paper:   #f5f1e8;  /* page background */
  --paper-2: #ece6d6;  /* card / hover surface */
  --ink:     #1a1916;  /* primary text */
  --ink-2:   #57544c;  /* secondary text */
  --ink-3:   #8a8678;  /* tertiary / mute */
  --rule:    #d9d2bf;  /* hairline */
  --rule-2:  #c5bda5;  /* heavier rule */
  --accent:  #1f6f7a;  /* deep teal */
  --accent-2:#0e4a52;  /* hover */
  --gold:    #b08a3a;  /* sparingly: section marks */
  --censor:  #b04331;  /* desaturated red — "things censors do" */
  --tech:    #8a6a1f;  /* ochre — "detection mechanics" */
  --defense: #2f6a4b;  /* moss green — "what we do back" */
  --code-bg: #ebe5d3;
}
@media (prefers-color-scheme: dark) {
  :root {
    --paper:   #15140f;
    --paper-2: #1f1d17;
    --ink:     #ece6d2;
    --ink-2:   #b6b09c;
    --ink-3:   #807a6a;
    --rule:    #2e2b22;
    --rule-2:  #423d30;
    --accent:  #5fb4bf;
    --accent-2:#86d0db;
    --gold:    #d4ad60;
    --censor:  #e07060;
    --tech:    #d6a85a;
    --defense: #6abf95;
    --code-bg: #211e16;
  }
}

* { box-sizing: border-box; }
html { font-size: 16px; -webkit-text-size-adjust: 100%; }
body {
  margin: 0;
  background: var(--paper);
  color: var(--ink);
  font-family: "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
  font-size: 1rem;
  line-height: 1.6;
  font-feature-settings: "ss01", "cv01";
  -webkit-font-smoothing: antialiased;
  text-rendering: optimizeLegibility;
}
.mono, code, pre, .row-id, .card-id, .tag {
  font-family: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
.display, .display-sm, h1, h2, h3 {
  font-family: "Fraunces", "Iowan Old Style", Georgia, serif;
  font-feature-settings: "ss01", "ss02";
  letter-spacing: -0.015em;
  line-height: 1.1;
}
.display { font-size: clamp(2.4rem, 5.5vw, 4.4rem); font-weight: 500; margin: 0; }
.display em { font-style: italic; color: var(--accent); font-feature-settings: "ss01"; }
.display-sm { font-size: clamp(1.6rem, 3vw, 2.2rem); font-weight: 500; margin: 0 0 0.6rem; }
h1 { font-size: clamp(1.75rem, 3vw, 2.4rem); font-weight: 500; margin: 0 0 0.5rem; }
h2 { font-size: 1.4rem; font-weight: 500; margin: 2.5rem 0 0.5rem; }
h3 { font-size: 1.15rem; font-weight: 500; margin: 0 0 0.3rem; }

a { color: var(--accent); text-decoration: none; transition: color 0.15s; }
a:hover { color: var(--accent-2); text-decoration: underline; }
em { font-style: italic; }
.muted { color: var(--ink-3); }
.lede { font-size: 1.15rem; line-height: 1.55; color: var(--ink-2); margin: 0.75rem 0; max-width: 42rem; }

code {
  background: var(--code-bg);
  padding: 0.05rem 0.3rem;
  border-radius: 3px;
  font-size: 0.9em;
}
pre {
  background: var(--code-bg);
  padding: 1rem 1.25rem;
  border-radius: 5px;
  overflow-x: auto;
  border: 1px solid var(--rule);
  font-size: 0.85rem;
  line-height: 1.6;
}
pre code { background: none; padding: 0; }

.wrap { max-width: 72rem; margin: 0 auto; padding: 0 1.5rem; }
main.wrap { padding: 2.5rem 1.5rem 5rem; }

/* Header */
.site-header { border-bottom: 1px solid var(--rule); background: var(--paper); position: sticky; top: 0; z-index: 10; backdrop-filter: blur(8px); background-color: color-mix(in oklab, var(--paper) 92%, transparent); }
.site-header .wrap { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 1rem; padding: 1rem 1.5rem; }
.brand { display: inline-flex; align-items: center; gap: 0.55rem; font-weight: 600; color: var(--ink); }
.brand:hover { color: var(--ink); text-decoration: none; }
.brand-mark { font-size: 1.3rem; color: var(--accent); }
.brand-name { font-family: "JetBrains Mono", monospace; font-size: 0.95rem; }
nav { display: flex; gap: 0.25rem 1.4rem; flex-wrap: wrap; align-items: center; }
nav a { color: var(--ink); font-size: 0.9rem; padding: 0.35rem 0; position: relative; }
nav a:hover { color: var(--accent); text-decoration: none; }
nav a:hover::after { content: ""; position: absolute; bottom: 0; left: 0; right: 0; height: 1px; background: var(--accent); }
nav a.external { color: var(--ink-2); }

/* Hero */
.hero { padding: 3.5rem 0 2.5rem; max-width: 50rem; }
.eyebrow { font-family: "JetBrains Mono", monospace; font-size: 0.78rem; letter-spacing: 0.08em; color: var(--gold); margin: 0 0 1rem; }
.cta { display: flex; flex-wrap: wrap; gap: 0.75rem; margin-top: 1.75rem; }
.btn { display: inline-flex; align-items: center; gap: 0.4rem; padding: 0.65rem 1.1rem; border-radius: 4px; font-size: 0.95rem; font-weight: 500; transition: all 0.15s; }
.btn.primary { background: var(--ink); color: var(--paper); }
.btn.primary:hover { background: var(--accent); color: var(--paper); text-decoration: none; }
.btn.ghost { border: 1px solid var(--rule-2); color: var(--ink); background: transparent; }
.btn.ghost:hover { border-color: var(--ink); color: var(--ink); text-decoration: none; }

.counts-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 1rem; margin: 3rem 0 0; padding: 1.5rem 0; border-top: 1px solid var(--rule); border-bottom: 1px solid var(--rule); }
.counts-grid div { display: flex; flex-direction: column; gap: 0.2rem; }
.counts-grid dt { font-family: "JetBrains Mono", monospace; font-size: 0.75rem; letter-spacing: 0.06em; text-transform: uppercase; color: var(--ink-3); margin: 0; }
.counts-grid dd { font-family: "Fraunces", serif; font-size: 2rem; font-weight: 500; margin: 0; color: var(--ink); }

/* Section marks */
.section-mark { font-family: "JetBrains Mono", monospace; font-size: 0.78rem; letter-spacing: 0.08em; color: var(--gold); margin: 4rem 0 1rem; padding-top: 2rem; border-top: 1px solid var(--rule); }

/* Two-column "why" section */
.two-col { display: grid; grid-template-columns: 1fr; gap: 2rem; }
@media (min-width: 60rem) { .two-col { grid-template-columns: 2fr 1fr; gap: 3rem; } }
.aside { padding: 1.5rem; background: var(--paper-2); border-left: 3px solid var(--gold); border-radius: 0 4px 4px 0; }
.aside-label { font-family: "JetBrains Mono", monospace; font-size: 0.75rem; letter-spacing: 0.08em; color: var(--gold); text-transform: uppercase; margin: 0 0 0.6rem; }
.aside p:last-child { margin-bottom: 0; }

/* Paper cards */
.paper-cards { list-style: none; padding: 0; margin: 1.5rem 0 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(20rem, 1fr)); gap: 1rem; }
.paper-card { background: var(--paper-2); border-radius: 5px; border: 1px solid var(--rule); transition: all 0.15s; overflow: hidden; }
.paper-card:hover { border-color: var(--accent); transform: translateY(-1px); box-shadow: 0 4px 12px rgba(0,0,0,0.04); }
.card-link { display: block; padding: 1rem 1.1rem; color: var(--ink); }
.card-link:hover { text-decoration: none; color: var(--ink); }
.card-id { font-size: 0.7rem; color: var(--ink-3); margin-bottom: 0.4rem; letter-spacing: -0.02em; }
.paper-card h3 { font-family: "Fraunces", serif; font-size: 1.05rem; font-weight: 500; margin: 0 0 0.4rem; color: var(--ink); line-height: 1.25; }
.card-meta { font-size: 0.85rem; color: var(--ink-2); margin-bottom: 0.6rem; }
.card-meta em { color: var(--ink-2); }
.card-tags { display: flex; flex-wrap: wrap; gap: 0.3rem; }

/* Paper list (compact rows) */
.paper-list { list-style: none; padding: 0; margin: 1.5rem 0 0; }
.paper-list li { border-bottom: 1px solid var(--rule); }
.paper-list li:first-child { border-top: 1px solid var(--rule); }
.paper-list li a { display: grid; grid-template-columns: minmax(15rem, 18rem) 1fr auto; gap: 1.5rem; padding: 1rem 0.5rem; color: var(--ink); align-items: baseline; transition: background 0.15s; }
.paper-list li a:hover { background: var(--paper-2); text-decoration: none; }
.row-id { font-size: 0.78rem; color: var(--ink-3); }
.row-title { font-family: "Fraunces", serif; font-size: 1.05rem; line-height: 1.3; color: var(--ink); }
.row-meta { font-size: 0.85rem; color: var(--ink-2); white-space: nowrap; }
@media (max-width: 50rem) {
  .paper-list li a { grid-template-columns: 1fr; gap: 0.25rem; }
  .row-meta { white-space: normal; }
}

/* Bottom CTA */
.cta-bottom { text-align: center; padding: 4rem 0 2rem; margin-top: 4rem; border-top: 1px solid var(--rule); }
.cta-bottom .lede { margin: 0.75rem auto 1.75rem; }

/* Tag chips — controlled vocabulary, the visual heart of the site */
.tag {
  display: inline-flex;
  align-items: center;
  padding: 0.1rem 0.5rem;
  margin: 0.15rem 0.2rem 0.15rem 0;
  background: var(--paper-2);
  border: 1px solid var(--rule);
  border-radius: 3px;
  font-size: 0.78rem;
  text-decoration: none;
  color: var(--ink);
  transition: all 0.12s;
  letter-spacing: -0.01em;
  white-space: nowrap;
}
.tag:hover { background: var(--ink); color: var(--paper); border-color: var(--ink); text-decoration: none; }
.tag.censor    { border-left: 3px solid var(--censor); padding-left: 0.45rem; }
.tag.technique { border-left: 3px solid var(--tech); padding-left: 0.45rem; }
.tag.defense   { border-left: 3px solid var(--defense); padding-left: 0.45rem; }

/* Paper detail page */
article.paper { max-width: 48rem; margin: 0 auto; }
article.paper .paper-id { font-size: 0.78rem; color: var(--ink-3); margin: 0 0 0.5rem; letter-spacing: -0.02em; }
article.paper h1 { font-size: clamp(1.6rem, 3vw, 2.4rem); font-weight: 500; margin: 0 0 0.5rem; }
article.paper .paper-links { font-size: 0.92rem; color: var(--ink-2); margin: 0 0 2rem; padding-bottom: 1.5rem; border-bottom: 1px solid var(--rule); }
.related-section { max-width: 48rem; margin: 3rem auto 0; }
.tag-name { color: var(--accent); font-size: 0.85em; }
.byline { font-size: 1rem; color: var(--ink-2); margin: 0 0 1.5rem; }
.byline em { font-style: italic; }
.badge { display: inline-block; font-family: "JetBrains Mono", monospace; font-size: 0.7rem; letter-spacing: 0.06em; text-transform: uppercase; padding: 0.1rem 0.5rem; border-radius: 3px; background: var(--gold); color: var(--paper); vertical-align: middle; margin-left: 0.5rem; }
.badge.core { background: var(--accent); color: var(--paper); }
.abstract, .notes { white-space: pre-wrap; }
.notes { padding: 1.25rem 1.4rem; background: var(--paper-2); border-left: 3px solid var(--accent); border-radius: 0 4px 4px 0; }
.tags-dl { display: grid; grid-template-columns: max-content 1fr; gap: 0.4rem 1.5rem; margin: 1rem 0; }
.tags-dl dt { font-family: "JetBrains Mono", monospace; font-size: 0.78rem; letter-spacing: 0.04em; color: var(--ink-3); padding-top: 0.3rem; }
.tags-dl dd { margin: 0; padding: 0; }

/* Tag index page */
.tag-index { list-style: none; padding: 0; display: grid; grid-template-columns: max-content max-content 1fr max-content; gap: 0.5rem 1.5rem; }
.tag-index li { display: contents; }
.tag-index li > a { font-family: "JetBrains Mono", monospace; font-size: 0.9rem; }
.tag-index li > span:nth-child(2) { color: var(--ink); }
.tag-index li > span.muted { color: var(--ink-3); font-size: 0.85rem; }

/* Taxonomy page */
.tax { display: grid; grid-template-columns: max-content 1fr; gap: 0.4rem 1.5rem; margin: 1rem 0; }
.tax dt { font-family: "JetBrains Mono", monospace; font-size: 0.85rem; padding-top: 0.2rem; }
.tax dd { margin: 0; padding: 0 0 0.4rem; color: var(--ink-2); font-size: 0.95rem; }
.tax dd .muted { display: block; font-size: 0.82rem; margin-top: 0.15rem; }
@media (max-width: 50rem) {
  .tax, .tags-dl, .tag-index { grid-template-columns: 1fr; gap: 0.2rem; }
  .tax dt, .tags-dl dt { padding-top: 0.5rem; }
}

/* Footer */
.site-footer { border-top: 1px solid var(--rule); margin-top: 4rem; padding: 3rem 0 2rem; background: var(--paper-2); color: var(--ink-2); font-size: 0.9rem; }
.foot-grid { display: grid; grid-template-columns: 2fr repeat(3, 1fr); gap: 2rem; margin-bottom: 2rem; }
@media (max-width: 50rem) { .foot-grid { grid-template-columns: 1fr 1fr; } }
.foot-title { font-family: "JetBrains Mono", monospace; font-size: 0.78rem; letter-spacing: 0.08em; text-transform: uppercase; color: var(--ink); margin-bottom: 0.5rem; }
.foot-grid ul { list-style: none; padding: 0; margin: 0; }
.foot-grid li { margin-bottom: 0.3rem; }
.foot-grid a { color: var(--ink-2); }
.foot-grid a:hover { color: var(--accent); }
.legal { padding-top: 1.5rem; border-top: 1px solid var(--rule); color: var(--ink-3); font-size: 0.82rem; max-width: 60rem; }
.legal a { color: var(--ink-2); }

/* Use page sections */
dl.tax dt code { font-family: inherit; background: none; }

/* Search */
.search-wrap { position: relative; flex: 1 1 28rem; max-width: 32rem; min-width: 14rem; margin: 0 1rem; }
#search {
  width: 100%;
  padding: 0.55rem 0.85rem;
  padding-right: 2rem;
  border: 1px solid var(--rule-2);
  border-radius: 4px;
  background: var(--paper);
  color: var(--ink);
  font-family: inherit;
  font-size: 0.92rem;
  transition: border-color 0.15s;
}
#search:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px color-mix(in oklab, var(--accent) 18%, transparent); }
.search-kbd {
  position: absolute; right: 0.6rem; top: 50%; transform: translateY(-50%);
  font-family: "JetBrains Mono", monospace; font-size: 0.75rem;
  padding: 0.05rem 0.4rem;
  border: 1px solid var(--rule-2);
  border-radius: 3px;
  color: var(--ink-3); background: var(--paper-2);
  pointer-events: none;
}
#search:focus + .search-kbd { display: none; }
#search-results {
  position: absolute; top: calc(100% + 0.5rem); left: 0; right: 0;
  background: var(--paper); border: 1px solid var(--rule-2); border-radius: 5px;
  box-shadow: 0 8px 24px rgba(0,0,0,0.08);
  max-height: 70vh; overflow-y: auto;
  z-index: 20;
}
#search-results .empty { padding: 1rem 1.1rem; color: var(--ink-3); font-size: 0.92rem; }
#search-results .summary { padding: 0.5rem 1.1rem; color: var(--ink-3); font-size: 0.78rem; letter-spacing: 0.04em; text-transform: uppercase; border-bottom: 1px solid var(--rule); font-family: "JetBrains Mono", monospace; }
#search-results a {
  display: grid; grid-template-columns: 1fr auto;
  gap: 0.4rem 1rem; padding: 0.65rem 1.1rem;
  color: var(--ink); border-bottom: 1px solid var(--rule);
}
#search-results a:last-child { border-bottom: none; }
#search-results a:hover, #search-results a.active { background: var(--paper-2); text-decoration: none; }
#search-results .r-title { font-family: "Fraunces", serif; font-size: 1rem; font-weight: 500; line-height: 1.25; }
#search-results .r-meta { font-size: 0.8rem; color: var(--ink-3); white-space: nowrap; }
#search-results .r-id { grid-column: 1 / 3; font-family: "JetBrains Mono", monospace; font-size: 0.72rem; color: var(--ink-3); }
#search-results .r-tags { grid-column: 1 / 3; display: flex; gap: 0.3rem; flex-wrap: wrap; }
#search-results .r-tags .tag { font-size: 0.7rem; padding: 0 0.4rem; }
#search-results mark { background: color-mix(in oklab, var(--gold) 30%, transparent); color: var(--ink); padding: 0; border-radius: 2px; }
@media (max-width: 60rem) {
  .site-header .wrap { flex-direction: column; align-items: stretch; }
  .search-wrap { margin: 0; max-width: none; }
}
`
