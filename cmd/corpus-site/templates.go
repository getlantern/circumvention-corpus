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
<meta name="color-scheme" content="dark">
<meta name="theme-color" content="#14120d">
<title>{{.Title}}</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Anton&family=Atkinson+Hyperlegible:ital,wght@0,400;0,700;1,400;1,700&family=JetBrains+Mono:wght@400;600&family=Special+Elite&display=swap" rel="stylesheet">
<link rel="stylesheet" href="/style.css">
<script src="/search.js" defer></script>
</head>
<body>
<div class="grain" aria-hidden="true"></div>
<header class="site-header">
  <div class="wrap">
    <a class="brand" href="/">
      <span class="brand-mark" aria-hidden="true">▮▮</span>
      <span class="brand-name">CIRCUMVENTION//CORPUS</span>
    </a>
    <div class="search-wrap">
      <span class="search-prompt" aria-hidden="true">$</span>
      <input id="search" type="search" placeholder="grep papers — active probing, GFW, Iran 2025…" autocomplete="off" spellcheck="false">
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
  <p class="eyebrow"><span class="stamp-mark" aria-hidden="true">▸</span> TRANSMISSION 0001 · FIELD STATION OPEN · LLM-CALLABLE</p>
  <h1 class="display">The field's literature.<br><span class="redact" aria-hidden="true">▮▮▮▮▮</span><em> Indexed.</em></h1>
  <p class="lede">A controlled-vocabulary corpus of censorship-circumvention research. Every paper tagged against a shared taxonomy of <a href="/censors/">censors</a>, <a href="/techniques/">detection techniques</a>, and <a href="/defenses/">defenses</a>. An MCP server exposes the whole thing to any AI assistant.</p>
  <div class="cta">
    <a class="btn primary" href="/use/">▸ Install the MCP server</a>
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
  <p class="section-mark"><span class="exhibit">EXHIBIT</span> <span class="exhibit-num">№01</span> <span class="exhibit-rule"></span> WHY THIS EXISTS</p>
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
  <p class="section-mark"><span class="exhibit">EXHIBIT</span> <span class="exhibit-num">№02</span> <span class="exhibit-rule"></span> CORE PAPERS</p>
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
  <p class="section-mark"><span class="exhibit">EXHIBIT</span> <span class="exhibit-num">№03</span> <span class="exhibit-rule"></span> RECENT ADDITIONS</p>
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
  <p class="section-mark"><span class="exhibit">EXHIBIT</span> <span class="exhibit-num">№04</span> <span class="exhibit-rule"></span> JOIN THE NETWORK</p>
  <h2 class="display-sm">Plug it into your assistant.</h2>
  <p class="lede">One install. Your AI gains <code>search_papers</code>, <code>get_paper</code>, <code>list_taxonomy</code>, and <code>find_related</code> over the corpus.</p>
  <a class="btn primary" href="/use/">▸ How to install</a>
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
  <p class="section-mark"><span class="exhibit-rule short"></span> REFERENCES IN THIS CORPUS</p>
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
  <p class="section-mark"><span class="exhibit-rule short"></span> RELATED PAPERS</p>
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

<p class="section-mark"><span class="exhibit-rule short"></span> {{len .Papers}} PAPER{{if ne (len .Papers) 1}}S{{end}} ON FILE</p>
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

<p>The corpus is designed to be useful in several ways. Pick whichever fits your workflow.</p>

<h2>1. Plug into your AI assistant (one line, hosted)</h2>

<p>The fastest path. The corpus runs as a hosted MCP server at <code>corpus.lantern.io/mcp</code>. Zero install, no toolchain, always reflects the latest committed state of the repo (auto-deploys on every push to <code>main</code>).</p>

<h3>Claude Code</h3>
<pre><code>claude mcp add --transport http -s user circumvention-corpus https://corpus.lantern.io/mcp
</code></pre>
<p>Verify with <code>claude mcp list</code>; it should show <code>✓ Connected</code>. The server's four tools become available in any conversation.</p>

<h3>Claude Desktop</h3>
<p>Edit your config (<code>~/Library/Application Support/Claude/claude_desktop_config.json</code> on macOS, <code>%APPDATA%/Claude/claude_desktop_config.json</code> on Windows):</p>
<pre><code>{
  "mcpServers": {
    "circumvention-corpus": {
      "url": "https://corpus.lantern.io/mcp",
      "transport": "http"
    }
  }
}
</code></pre>
<p>Restart Claude Desktop.</p>

<h3>Cursor / VS Code Copilot / other MCP clients</h3>
<p>Any MCP-compliant client takes a URL via the Streamable HTTP transport. Same shape — drop the URL above into your client's MCP config.</p>

<h2>2. Browse this site</h2>
<p>Every paper has a stable URL: <code>/papers/&lt;id&gt;/</code>. Tag indexes (<a href="/censors/">censors</a>, <a href="/techniques/">techniques</a>, <a href="/defenses/">defenses</a>) let you walk the field by axis. The whole site rebuilds from the YAML on every push to <code>main</code>; whatever you see here matches the source repo.</p>

<h2>3. Read the YAML directly</h2>
<p>Every paper is a small YAML file in <a href="https://github.com/getlantern/circumvention-corpus/tree/main/corpus/papers">corpus/papers/</a>. The <a href="https://github.com/getlantern/circumvention-corpus/blob/main/schema/paper.schema.json">JSON schema</a> documents every field. The <a href="/taxonomy/">taxonomy</a> documents the controlled-vocabulary IDs that tag fields use. If you're building your own tooling on top of the corpus, this is the most boring, most stable interface — clone the repo, walk the directory.</p>

<pre><code>git clone https://github.com/getlantern/circumvention-corpus
cd circumvention-corpus
ls corpus/papers/                       # one YAML per paper
yq '.censors' corpus/papers/2023-wu-fully-encrypted-detect.yaml
</code></pre>

<h2>4. Self-host the MCP server (offline / privacy)</h2>

<p>For users behind aggressive censorship who can't reach Cloudflare, or anyone who'd rather not send queries off-machine. The corpus ships a Go MCP server with stdio transport — single binary, no runtime deps.</p>

<pre><code>go install github.com/getlantern/circumvention-corpus/cmd/corpus-mcp@latest

# Then register it. The --corpus flag points at a local clone of the repo.
git clone https://github.com/getlantern/circumvention-corpus ~/code/circumvention-corpus
claude mcp add -s user circumvention-corpus \
  $(go env GOPATH)/bin/corpus-mcp -- --corpus $HOME/code/circumvention-corpus
</code></pre>

<h2>What the MCP server exposes</h2>
<p>Four tools, designed to compose:</p>
<dl class="tax">
  <dt><span class="mono">search_papers</span></dt>
  <dd>Keyword + tag-filter search. Filters: <code>censors</code>, <code>techniques</code>, <code>defenses</code>, <code>year_min</code>, <code>year_max</code>, <code>venue</code>, <code>core_only</code>. Returns ranked records with abstract, tags, and team notes.</dd>
  <dt><span class="mono">get_paper</span></dt>
  <dd>Full record for a single paper id, plus any extracted findings tagged to it. Use after <code>search_papers</code> when the agent needs the full notes / references / metadata.</dd>
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
 * "FIELD STATION 01: THE WALL"
 *
 * Concrete-wall base (dark, gritty), cream wheatpaste poster cards
 * stuck on top with masking-tape corners, stenciled headlines in
 * Anton with resistance-red spray-paint accents, terminal-style
 * search and data display.
 *
 * Typography:
 *   Anton — display / poster headlines (heavy condensed, all caps)
 *   Atkinson Hyperlegible — body (distinctive humanist grotesque)
 *   JetBrains Mono — IDs, controlled vocabulary, terminal data
 *   Special Elite — typewriter-stamp accents (eyebrows, evidence)
 *
 * No light/dark toggle — the wall is the wall. The cream paper cards
 * carry the readable typography; the wall provides the atmosphere.
 */

:root {
  --wall:        #14120d;  /* dark concrete wall */
  --wall-2:      #1f1c14;  /* lighter wall — sticky-note layer */
  --wall-3:      #2a2618;  /* warmer panel */
  --paper:       #efe4cc;  /* wheatpaste poster cream */
  --paper-2:     #e6d8b9;  /* paper hover / tinted */
  --paper-edge:  #c9b896;  /* paper torn edge */
  --ink:         #0a0907;  /* street-stencil black */
  --ink-2:       #1c1812;  /* body */
  --ink-3:       #5a5241;  /* tertiary / muted on paper */
  --paper-mute:  #8a7e63;  /* secondary text on paper */
  --wall-mute:   #8b7f64;  /* secondary text on wall */
  --wall-mute-2: #b9a880;  /* primary text on wall */
  --accent:      #e63946;  /* RESISTANCE RED — spray paint, alert */
  --accent-2:    #ff5963;  /* lighter red — hover */
  --accent-3:    #b62633;  /* darker red — pressed / trim */
  --phosphor:    #7cff8c;  /* terminal CRT green */
  --caution:     #f4d03f;  /* caution-tape yellow */
  --rule:        #2e2a1f;  /* wall hairline */
  --rule-2:      #3d3826;  /* heavier wall rule */
  --paper-rule:  #b9a880;  /* paper hairline */
  --code-bg:     #14120d;  /* terminal block */
  --code-fg:     #e0d5b9;
}

* { box-sizing: border-box; }
html { font-size: 16px; -webkit-text-size-adjust: 100%; background: var(--wall); }
body {
  margin: 0;
  background: var(--wall);
  color: var(--wall-mute-2);
  font-family: "Atkinson Hyperlegible", -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
  font-size: 1rem;
  line-height: 1.6;
  -webkit-font-smoothing: antialiased;
  text-rendering: optimizeLegibility;
  background-image:
    radial-gradient(ellipse at 20% 10%, color-mix(in oklab, var(--accent) 7%, transparent) 0%, transparent 55%),
    radial-gradient(ellipse at 80% 90%, color-mix(in oklab, var(--phosphor) 4%, transparent) 0%, transparent 60%);
  background-attachment: fixed;
  position: relative;
}

/* Concrete grain overlay — fixed full-screen SVG turbulence at very low
 * opacity. Sits below content (z-index:1, content gets >=2). The pointer
 * events are off so it never intercepts clicks. */
.grain {
  position: fixed; inset: 0;
  pointer-events: none;
  z-index: 1;
  opacity: 0.10;
  mix-blend-mode: overlay;
  background-image: url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='220' height='220'><filter id='n'><feTurbulence type='fractalNoise' baseFrequency='0.9' numOctaves='2' stitchTiles='stitch'/><feColorMatrix values='0 0 0 0 0  0 0 0 0 0  0 0 0 0 0  0 0 0 0.7 0'/></filter><rect width='100%' height='100%' filter='url(%23n)'/></svg>");
}

.site-header, main.wrap, .site-footer { position: relative; z-index: 2; }

.mono, code, pre, .row-id, .card-id, .tag, .paper-id {
  font-family: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
.display, .display-sm {
  font-family: "Anton", "Oswald", "Helvetica Neue Condensed Bold", Impact, sans-serif;
  text-transform: uppercase;
  letter-spacing: 0.005em;
  line-height: 0.95;
  font-weight: 400;
}
h1, h2, h3 {
  font-family: "Atkinson Hyperlegible", -apple-system, system-ui, sans-serif;
  letter-spacing: -0.01em;
  line-height: 1.2;
  font-weight: 700;
}
.display { font-size: clamp(3rem, 8vw, 6rem); margin: 0; color: var(--paper); }
.display em { font-style: normal; color: var(--accent); position: relative; }
/* Spray-paint drip on the accented word — a soft red glow underneath. */
.display em::after {
  content: "";
  position: absolute; left: 0; right: 0; bottom: -0.05em; height: 0.18em;
  background: var(--accent);
  filter: blur(8px);
  opacity: 0.55;
  z-index: -1;
}
.display .redact {
  display: inline-block;
  background: var(--ink);
  color: var(--ink);
  margin-right: 0.3em;
  padding: 0 0.05em;
  letter-spacing: -0.05em;
}
.display-sm { font-size: clamp(1.7rem, 3.6vw, 2.6rem); margin: 0 0 0.7rem; color: var(--paper); }
h1 { font-size: clamp(1.75rem, 3vw, 2.4rem); margin: 0 0 0.5rem; color: var(--paper); }
h2 { font-size: 1.3rem; margin: 2.5rem 0 0.6rem; color: var(--paper); text-transform: uppercase; letter-spacing: 0.02em; font-family: "Anton", Impact, sans-serif; font-weight: 400; }
h3 { font-size: 1.1rem; margin: 0 0 0.3rem; color: var(--paper); font-weight: 700; }

a { color: var(--accent); text-decoration: none; transition: color 0.15s; border-bottom: 1px dashed transparent; }
a:hover { color: var(--accent-2); border-bottom-color: var(--accent-2); }
em { font-style: italic; }
.muted { color: var(--wall-mute); }
.lede { font-size: 1.1rem; line-height: 1.6; color: var(--wall-mute-2); margin: 0.75rem 0; max-width: 42rem; }

code {
  background: var(--wall-2);
  color: var(--phosphor);
  padding: 0.05rem 0.4rem;
  border: 1px solid var(--rule-2);
  font-size: 0.88em;
  font-weight: 600;
}
pre {
  background: var(--wall-2);
  color: var(--phosphor);
  padding: 1rem 1.25rem;
  overflow-x: auto;
  border: 1px solid var(--rule-2);
  border-left: 3px solid var(--phosphor);
  font-size: 0.85rem;
  line-height: 1.6;
  position: relative;
}
pre::before {
  content: "// terminal";
  position: absolute; top: 0.4rem; right: 0.7rem;
  font-size: 0.65rem; letter-spacing: 0.1em;
  color: var(--wall-mute);
  font-family: "Special Elite", "Courier New", monospace;
  text-transform: uppercase;
}
pre code { background: none; padding: 0; border: none; color: inherit; }

.wrap { max-width: 76rem; margin: 0 auto; padding: 0 1.5rem; }
main.wrap { padding: 3rem 1.5rem 5rem; }

/* ───────────────────────── HEADER ───────────────────────── */
.site-header {
  background: color-mix(in oklab, var(--wall) 88%, transparent);
  backdrop-filter: blur(10px);
  -webkit-backdrop-filter: blur(10px);
  border-bottom: 1px solid var(--rule-2);
  position: sticky; top: 0;
  box-shadow: 0 0 0 0 transparent, 0 1px 0 var(--accent), 0 2px 0 var(--ink);
}
.site-header .wrap {
  display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between;
  gap: 1rem; padding: 0.85rem 1.5rem;
}
.brand { display: inline-flex; align-items: center; gap: 0.5rem; color: var(--paper); border: none; }
.brand:hover { color: var(--paper); border: none; }
.brand-mark {
  color: var(--accent);
  font-size: 1rem;
  letter-spacing: -0.15em;
  text-shadow: 0 0 8px color-mix(in oklab, var(--accent) 40%, transparent);
}
.brand-name {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.92rem; font-weight: 600;
  letter-spacing: 0.02em;
}
nav { display: flex; gap: 0.25rem 1.4rem; flex-wrap: wrap; align-items: center; }
nav a {
  color: var(--wall-mute-2); font-size: 0.82rem;
  text-transform: uppercase; letter-spacing: 0.08em;
  padding: 0.25rem 0; position: relative;
  font-family: "JetBrains Mono", monospace;
  border: none;
}
nav a:hover { color: var(--accent); border: none; }
nav a:hover::after {
  content: ""; position: absolute; bottom: -2px; left: 0; right: 0; height: 2px;
  background: var(--accent);
}
nav a.external { color: var(--wall-mute); }

/* ───────────────────────── HERO ───────────────────────── */
.hero { padding: 4rem 0 3rem; max-width: 56rem; }
.eyebrow {
  font-family: "Special Elite", "Courier New", monospace;
  font-size: 0.85rem; letter-spacing: 0.05em;
  color: var(--accent); margin: 0 0 1.4rem;
  text-transform: uppercase;
  display: inline-block;
  padding: 0.35rem 0.7rem 0.3rem;
  border: 1px solid var(--accent);
  background: color-mix(in oklab, var(--accent) 8%, transparent);
}
.eyebrow .stamp-mark { color: var(--accent); margin-right: 0.4rem; }
.cta { display: flex; flex-wrap: wrap; gap: 0.85rem; margin-top: 2rem; }
.btn {
  display: inline-flex; align-items: center; gap: 0.4rem;
  padding: 0.75rem 1.3rem;
  font-size: 0.85rem; font-weight: 700;
  font-family: "JetBrains Mono", monospace;
  text-transform: uppercase; letter-spacing: 0.08em;
  border: 2px solid; transition: all 0.12s;
}
.btn.primary {
  background: var(--accent); color: var(--paper);
  border-color: var(--accent);
  box-shadow: 4px 4px 0 var(--ink);
}
.btn.primary:hover {
  background: var(--paper); color: var(--ink);
  border-color: var(--paper);
  transform: translate(-2px, -2px);
  box-shadow: 6px 6px 0 var(--accent);
  border-bottom-color: var(--paper);
}
.btn.ghost {
  background: transparent; color: var(--paper);
  border-color: var(--paper);
}
.btn.ghost:hover {
  background: var(--paper); color: var(--ink);
  border-bottom-color: var(--ink);
}

.counts-grid {
  display: grid; grid-template-columns: repeat(4, 1fr); gap: 0;
  margin: 3.5rem 0 0;
  border: 1px solid var(--rule-2);
  background: var(--wall-2);
}
.counts-grid div {
  display: flex; flex-direction: column; gap: 0.3rem;
  padding: 1.4rem 1.4rem;
  border-right: 1px solid var(--rule-2);
}
.counts-grid div:last-child { border-right: none; }
.counts-grid dt {
  font-family: "Special Elite", "Courier New", monospace;
  font-size: 0.72rem; letter-spacing: 0.12em;
  text-transform: uppercase; color: var(--wall-mute);
  margin: 0;
}
.counts-grid dd {
  font-family: "Anton", Impact, sans-serif;
  font-size: 2.6rem; line-height: 1; margin: 0;
  color: var(--paper);
}
@media (max-width: 50rem) {
  .counts-grid { grid-template-columns: repeat(2, 1fr); }
  .counts-grid div:nth-child(2) { border-right: none; }
  .counts-grid div:nth-child(1), .counts-grid div:nth-child(2) { border-bottom: 1px solid var(--rule-2); }
}

/* ───────────────────── SECTION MARKS ─────────────────────
 * Typewriter "EXHIBIT №01 ━━━━━━ TITLE" pattern. The exhibit
 * label is rotated stamp-style for the wheatpaste-poster vibe. */
.section-mark {
  display: flex; align-items: center; gap: 0.7rem;
  font-family: "Special Elite", "Courier New", monospace;
  font-size: 0.92rem; letter-spacing: 0.06em;
  color: var(--paper);
  margin: 4.5rem 0 1.5rem; padding-top: 2rem;
  text-transform: uppercase;
  border-top: 1px dashed var(--rule-2);
  position: relative;
}
.section-mark .exhibit {
  display: inline-block;
  padding: 0.18rem 0.5rem 0.12rem;
  background: var(--accent); color: var(--paper);
  font-size: 0.75rem; letter-spacing: 0.1em;
  transform: rotate(-1.5deg);
  font-weight: 700;
  box-shadow: 2px 2px 0 var(--ink);
}
.section-mark .exhibit-num {
  font-family: "Anton", Impact, sans-serif;
  font-size: 1.4rem; color: var(--paper);
  letter-spacing: 0;
}
.section-mark .exhibit-rule {
  flex: 1; height: 4px;
  background: repeating-linear-gradient(
    -45deg,
    var(--caution) 0, var(--caution) 6px,
    var(--ink) 6px, var(--ink) 12px
  );
  max-width: 8rem;
  align-self: center;
}
.section-mark .exhibit-rule.short { max-width: 4rem; }

/* ───────────────────── TWO-COL "WHY" ──────────────────── */
.two-col { display: grid; grid-template-columns: 1fr; gap: 2rem; }
@media (min-width: 60rem) { .two-col { grid-template-columns: 2fr 1fr; gap: 3.5rem; } }
.aside {
  padding: 1.5rem 1.6rem;
  background: var(--wall-2);
  border: 1px solid var(--rule-2);
  border-left: 4px solid var(--phosphor);
  position: relative;
  transform: rotate(0.3deg);
}
.aside::before {
  content: "// SIDEBAR";
  position: absolute; top: -0.7rem; left: 1rem;
  background: var(--wall); padding: 0 0.4rem;
  font-family: "JetBrains Mono", monospace;
  font-size: 0.7rem; color: var(--phosphor);
  letter-spacing: 0.1em;
}
.aside-label {
  font-family: "Special Elite", "Courier New", monospace;
  font-size: 0.78rem; letter-spacing: 0.08em;
  color: var(--phosphor); text-transform: uppercase;
  margin: 0 0 0.6rem;
}
.aside p { color: var(--wall-mute-2); }
.aside p:last-child { margin-bottom: 0; }

/* ─────────────── PAPER CARDS — wheatpaste posters ───────────────
 * Each card is a cream poster pasted onto the wall. Slight rotation
 * alternates by nth-child so the grid breathes. Masking-tape strips
 * appear as ::before pseudo-elements at the top corners. */
.paper-cards {
  list-style: none; padding: 1.5rem 0 0; margin: 1rem 0 0;
  display: grid; grid-template-columns: repeat(auto-fill, minmax(20rem, 1fr));
  gap: 1.5rem 1.2rem;
}
.paper-card {
  background: var(--paper); color: var(--ink);
  border: 1px solid var(--paper-edge);
  position: relative;
  transition: transform 0.18s, box-shadow 0.18s;
  box-shadow: 4px 4px 0 var(--ink), 4px 4px 0 1px var(--rule-2);
}
.paper-card:nth-child(odd)  { transform: rotate(-0.4deg); }
.paper-card:nth-child(even) { transform: rotate(0.3deg); }
.paper-card:nth-child(3n)   { transform: rotate(0.2deg); }
.paper-card::before {
  content: "";
  position: absolute; top: -8px; left: 14%;
  width: 72px; height: 18px;
  background: color-mix(in oklab, var(--caution) 70%, transparent);
  border: 1px solid color-mix(in oklab, var(--caution) 90%, var(--ink));
  transform: rotate(-3deg);
  box-shadow: 1px 1px 2px rgba(0,0,0,0.3);
  z-index: 1;
}
.paper-card:nth-child(even)::before { left: auto; right: 12%; transform: rotate(4deg); background: color-mix(in oklab, var(--paper) 60%, transparent); border-color: var(--paper-edge); }
.paper-card:hover {
  transform: rotate(0deg) translateY(-2px);
  box-shadow: 6px 6px 0 var(--accent);
  z-index: 3;
}
.card-link { display: block; padding: 1.3rem 1.2rem 1.1rem; color: var(--ink); border: none; }
.card-link:hover { color: var(--ink); border: none; }
.card-id {
  font-size: 0.7rem; color: var(--ink-3);
  margin-bottom: 0.5rem; letter-spacing: 0.02em;
  text-transform: uppercase;
  border-bottom: 1px dashed var(--paper-edge);
  padding-bottom: 0.4rem;
}
.paper-card h3 {
  font-family: "Atkinson Hyperlegible", system-ui, sans-serif;
  font-size: 1.05rem; font-weight: 700;
  margin: 0 0 0.5rem; color: var(--ink); line-height: 1.25;
  letter-spacing: -0.005em;
}
.card-meta {
  font-size: 0.85rem; color: var(--paper-mute);
  margin-bottom: 0.7rem;
}
.card-meta em { color: var(--ink-3); font-style: italic; }
.card-tags { display: flex; flex-wrap: wrap; gap: 0.3rem; }

/* ─────────────── PAPER LIST — declassified docket rows ─────────── */
.paper-list { list-style: none; padding: 0; margin: 1.5rem 0 0; }
.paper-list li { border-bottom: 1px solid var(--rule); }
.paper-list li:first-child { border-top: 1px solid var(--rule-2); }
.paper-list li a {
  display: grid; grid-template-columns: minmax(15rem, 18rem) 1fr auto;
  gap: 1.5rem; padding: 1rem 0.75rem;
  color: var(--paper); border: none;
  align-items: baseline; transition: background 0.15s, color 0.15s;
}
.paper-list li a:hover {
  background: var(--wall-2); color: var(--accent);
  border: none;
}
.paper-list li a:hover .row-title { color: var(--paper); }
.paper-list li a:hover::before {
  content: "▸";
  color: var(--accent);
  position: absolute;
  margin-left: -1rem;
}
.paper-list li { position: relative; }
.row-id { font-size: 0.78rem; color: var(--wall-mute); }
.row-title {
  font-family: "Atkinson Hyperlegible", sans-serif;
  font-size: 1rem; font-weight: 700; line-height: 1.3;
  color: var(--paper); transition: color 0.15s;
}
.row-meta { font-size: 0.83rem; color: var(--wall-mute); white-space: nowrap; }
@media (max-width: 50rem) {
  .paper-list li a { grid-template-columns: 1fr; gap: 0.25rem; }
  .row-meta { white-space: normal; }
}

/* ──────────────── BOTTOM CTA ──────────────── */
.cta-bottom { text-align: center; padding: 4rem 0 2rem; margin-top: 5rem; }
.cta-bottom .section-mark { justify-content: center; border-top: none; padding-top: 0; }
.cta-bottom .lede { margin: 0.75rem auto 1.75rem; }

/* ──────────────── TAG CHIPS — rubber stamps ────────────────
 * Rectangular stamp borders, mono caps. Color-coded by category:
 * red = censor (the threat), yellow = technique (caution),
 * green = defense (resistance / phosphor terminal). */
.tag {
  display: inline-flex; align-items: center;
  padding: 0.18rem 0.55rem 0.14rem;
  margin: 0.2rem 0.25rem 0.2rem 0;
  background: transparent;
  border: 1px solid var(--ink-3);
  font-size: 0.7rem;
  letter-spacing: 0.05em;
  text-transform: uppercase;
  color: var(--ink); font-weight: 600;
  transition: all 0.12s;
  white-space: nowrap;
}
.tag:hover {
  background: var(--ink); color: var(--paper);
  border-color: var(--ink); transform: rotate(-1deg);
  border-bottom-color: var(--ink);
}
.tag.censor    { color: var(--accent-3); border-color: var(--accent-3); }
.tag.censor:hover    { background: var(--accent-3); color: var(--paper); border-color: var(--accent-3); }
.tag.technique { color: #997708; border-color: #997708; }
.tag.technique:hover { background: #997708; color: var(--paper); border-color: #997708; }
.tag.defense   { color: #186a3b; border-color: #186a3b; }
.tag.defense:hover   { background: #186a3b; color: var(--paper); border-color: #186a3b; }

/* On the dark wall (e.g. paper-list rows, search results), tags need
 * lighter colors to read against the dark bg. */
.paper-list .tag, #search-results .tag, .related-section .tag {
  color: var(--paper); border-color: var(--wall-mute);
}
.paper-list .tag.censor, #search-results .tag.censor { color: var(--accent-2); border-color: var(--accent-2); }
.paper-list .tag.technique, #search-results .tag.technique { color: var(--caution); border-color: var(--caution); }
.paper-list .tag.defense, #search-results .tag.defense { color: var(--phosphor); border-color: var(--phosphor); }

/* ──────────────── PAPER DETAIL — declassified document ──────────────── */
article.paper {
  max-width: 50rem; margin: 0 auto;
  background: var(--paper); color: var(--ink);
  padding: 3rem 3rem 2.5rem;
  border: 1px solid var(--paper-edge);
  position: relative;
  box-shadow: 6px 6px 0 var(--ink), 6px 6px 0 1px var(--accent);
}
article.paper::before {
  content: "DECLASSIFIED";
  position: absolute; top: 1.2rem; right: 1.5rem;
  font-family: "Special Elite", "Courier New", monospace;
  font-size: 1rem; letter-spacing: 0.1em;
  color: var(--accent);
  border: 2px solid var(--accent);
  padding: 0.25rem 0.65rem 0.18rem;
  transform: rotate(8deg);
  opacity: 0.85;
}
article.paper .paper-id {
  font-size: 0.72rem; color: var(--ink-3);
  margin: 0 0 0.7rem; letter-spacing: 0.06em;
  text-transform: uppercase;
}
article.paper h1 {
  font-family: "Atkinson Hyperlegible", sans-serif;
  font-size: clamp(1.6rem, 3vw, 2.2rem); font-weight: 700;
  margin: 0 0 0.6rem; color: var(--ink);
  letter-spacing: -0.015em; line-height: 1.2;
  text-transform: none; max-width: 90%;
}
article.paper h2 {
  font-family: "Anton", Impact, sans-serif;
  color: var(--ink); font-size: 1.2rem;
  margin: 2rem 0 0.6rem; letter-spacing: 0.04em;
}
article.paper h3 { color: var(--ink); }
article.paper p { color: var(--ink-2); }
article.paper a { color: var(--accent); }
article.paper a:hover { color: var(--accent-3); }
article.paper code { background: var(--wall); color: var(--phosphor); border-color: var(--ink); }
article.paper .paper-links {
  font-size: 0.92rem; color: var(--ink-3);
  margin: 0 0 2rem; padding-bottom: 1.5rem;
  border-bottom: 1px dashed var(--paper-edge);
}
.related-section { max-width: 50rem; margin: 3rem auto 0; }
.tag-name { color: var(--accent); }
.byline { font-size: 0.95rem; color: var(--paper-mute); margin: 0 0 1.5rem; }
.byline em { color: var(--ink-2); font-style: italic; }
.badge {
  display: inline-block;
  font-family: "Special Elite", "Courier New", monospace;
  font-size: 0.7rem; letter-spacing: 0.1em;
  text-transform: uppercase;
  padding: 0.2rem 0.55rem 0.14rem;
  background: var(--accent); color: var(--paper);
  vertical-align: middle; margin-left: 0.7rem;
  transform: rotate(-3deg); display: inline-block;
}
.badge.core { background: var(--accent); }
.abstract, .notes { white-space: pre-wrap; }
article.paper .abstract { color: var(--ink-2); }
article.paper .notes {
  padding: 1.25rem 1.4rem;
  background: var(--wall);
  color: var(--wall-mute-2);
  border: 1px solid var(--rule-2);
  border-left: 3px solid var(--phosphor);
  margin: 1rem 0;
  font-family: "JetBrains Mono", monospace;
  font-size: 0.88rem;
  position: relative;
}
article.paper .notes::before {
  content: "▸ INTERNAL NOTES";
  display: block; font-size: 0.7rem; color: var(--phosphor);
  letter-spacing: 0.1em; margin-bottom: 0.6rem;
  text-transform: uppercase;
}
.tags-dl {
  display: grid; grid-template-columns: max-content 1fr;
  gap: 0.6rem 1.5rem; margin: 1rem 0;
}
.tags-dl dt {
  font-family: "Special Elite", "Courier New", monospace;
  font-size: 0.75rem; letter-spacing: 0.1em;
  color: var(--accent); padding-top: 0.35rem;
  text-transform: uppercase;
}
.tags-dl dd { margin: 0; padding: 0; }

@media (max-width: 50rem) {
  article.paper { padding: 2rem 1.5rem; }
  article.paper::before { font-size: 0.78rem; padding: 0.2rem 0.5rem; }
}

/* ──────────────── TAG INDEX ──────────────── */
.tag-index {
  list-style: none; padding: 0; margin: 1.5rem 0;
  display: grid;
  grid-template-columns: max-content max-content 1fr max-content;
  gap: 0.6rem 1.5rem;
}
.tag-index li { display: contents; }
.tag-index li > a {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.92rem; color: var(--accent);
  border: none;
}
.tag-index li > a:hover { color: var(--accent-2); border: none; }
.tag-index li > span:nth-child(2) { color: var(--paper); font-weight: 600; }
.tag-index li > span.muted {
  color: var(--wall-mute); font-size: 0.82rem;
  font-family: "JetBrains Mono", monospace;
}

/* ──────────────── TAXONOMY PAGE ──────────────── */
.tax {
  display: grid; grid-template-columns: max-content 1fr;
  gap: 0.6rem 1.5rem; margin: 1rem 0;
}
.tax dt {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.88rem; padding-top: 0.25rem;
  color: var(--paper);
}
.tax dt a { color: var(--accent); border: none; }
.tax dt a:hover { color: var(--accent-2); border: none; }
.tax dd { margin: 0; padding: 0 0 0.4rem; color: var(--wall-mute-2); font-size: 0.95rem; }
.tax dd .muted { display: block; font-size: 0.82rem; margin-top: 0.15rem; color: var(--wall-mute); }
@media (max-width: 50rem) {
  .tax, .tags-dl, .tag-index { grid-template-columns: 1fr; gap: 0.2rem; }
  .tax dt, .tags-dl dt { padding-top: 0.6rem; }
}

/* ──────────────── FOOTER — classified document ──────────────── */
.site-footer {
  border-top: 1px solid var(--accent);
  margin-top: 5rem; padding: 0 0 1.5rem;
  background: var(--wall-2);
  color: var(--wall-mute-2); font-size: 0.88rem;
  position: relative;
}
.site-footer::before {
  content: "";
  display: block; height: 8px;
  background: repeating-linear-gradient(
    -45deg,
    var(--caution) 0, var(--caution) 14px,
    var(--ink) 14px, var(--ink) 28px
  );
}
.site-footer .wrap { padding-top: 3rem; }
.foot-grid {
  display: grid; grid-template-columns: 2fr repeat(3, 1fr);
  gap: 2.5rem; margin-bottom: 2rem;
}
@media (max-width: 50rem) { .foot-grid { grid-template-columns: 1fr 1fr; } }
.foot-title {
  font-family: "Special Elite", "Courier New", monospace;
  font-size: 0.78rem; letter-spacing: 0.12em;
  text-transform: uppercase; color: var(--accent);
  margin-bottom: 0.6rem;
}
.foot-grid ul { list-style: none; padding: 0; margin: 0; }
.foot-grid li { margin-bottom: 0.35rem; }
.foot-grid a { color: var(--wall-mute-2); border: none; }
.foot-grid a:hover { color: var(--accent); border: none; }
.legal {
  padding-top: 1.5rem; border-top: 1px dashed var(--rule-2);
  color: var(--wall-mute); font-size: 0.78rem;
  max-width: 70rem;
  font-family: "JetBrains Mono", monospace;
  letter-spacing: 0.02em;
}
.legal a { color: var(--wall-mute-2); border: none; }
.legal a:hover { color: var(--accent); }

/* Use page sections */
dl.tax dt code { font-family: inherit; background: none; border: none; padding: 0; color: inherit; }

/* ──────────────── SEARCH — terminal CRT ──────────────── */
.search-wrap {
  position: relative;
  flex: 1 1 28rem; max-width: 32rem; min-width: 14rem;
  margin: 0 1rem;
}
.search-prompt {
  position: absolute; left: 0.85rem; top: 50%;
  transform: translateY(-50%);
  color: var(--phosphor);
  font-family: "JetBrains Mono", monospace;
  font-size: 1rem; font-weight: 700;
  pointer-events: none; z-index: 2;
  text-shadow: 0 0 6px color-mix(in oklab, var(--phosphor) 50%, transparent);
}
#search {
  width: 100%;
  padding: 0.6rem 2.2rem 0.55rem 2rem;
  border: 1px solid var(--rule-2);
  background: var(--wall);
  color: var(--phosphor);
  font-family: "JetBrains Mono", monospace;
  font-size: 0.88rem;
  letter-spacing: 0.01em;
  transition: border-color 0.15s, box-shadow 0.15s;
  caret-color: var(--phosphor);
}
#search::placeholder { color: var(--wall-mute); }
#search:focus {
  outline: none;
  border-color: var(--phosphor);
  box-shadow: 0 0 0 1px var(--phosphor), 0 0 14px color-mix(in oklab, var(--phosphor) 35%, transparent);
}
.search-kbd {
  position: absolute; right: 0.6rem; top: 50%; transform: translateY(-50%);
  font-family: "JetBrains Mono", monospace; font-size: 0.7rem;
  padding: 0.1rem 0.4rem;
  border: 1px solid var(--rule-2);
  color: var(--wall-mute); background: var(--wall-2);
  pointer-events: none;
}
#search:focus + .search-kbd { display: none; }
#search-results {
  position: absolute; top: calc(100% + 0.5rem); left: 0; right: 0;
  background: var(--wall);
  border: 1px solid var(--phosphor);
  box-shadow: 0 12px 32px rgba(0,0,0,0.6), 0 0 0 1px color-mix(in oklab, var(--phosphor) 30%, transparent);
  max-height: 70vh; overflow-y: auto;
  z-index: 30;
}
#search-results .empty {
  padding: 1rem 1.1rem; color: var(--wall-mute);
  font-size: 0.92rem;
  font-family: "JetBrains Mono", monospace;
}
#search-results .empty::before { content: "// "; color: var(--accent); }
#search-results .summary {
  padding: 0.5rem 1.1rem;
  color: var(--phosphor); font-size: 0.72rem;
  letter-spacing: 0.1em; text-transform: uppercase;
  border-bottom: 1px solid var(--rule-2);
  font-family: "JetBrains Mono", monospace;
}
#search-results .summary::before { content: "▸ "; color: var(--phosphor); }
#search-results a {
  display: grid; grid-template-columns: 1fr auto;
  gap: 0.4rem 1rem; padding: 0.7rem 1.1rem;
  color: var(--paper);
  border-bottom: 1px solid var(--rule);
  border: none; border-bottom: 1px solid var(--rule);
}
#search-results a:last-child { border-bottom: none; }
#search-results a:hover, #search-results a.active {
  background: var(--wall-2); color: var(--accent);
}
#search-results .r-title {
  font-family: "Atkinson Hyperlegible", sans-serif;
  font-size: 0.95rem; font-weight: 700; line-height: 1.25;
  color: var(--paper);
}
#search-results a:hover .r-title, #search-results a.active .r-title { color: var(--accent); }
#search-results .r-meta { font-size: 0.78rem; color: var(--wall-mute); white-space: nowrap; }
#search-results .r-id {
  grid-column: 1 / 3;
  font-family: "JetBrains Mono", monospace;
  font-size: 0.7rem; color: var(--wall-mute);
}
#search-results .r-tags { grid-column: 1 / 3; display: flex; gap: 0.3rem; flex-wrap: wrap; }
#search-results .r-tags .tag { font-size: 0.65rem; padding: 0.05rem 0.4rem; }
#search-results mark {
  background: color-mix(in oklab, var(--caution) 50%, transparent);
  color: var(--paper); padding: 0;
  text-shadow: 0 0 4px color-mix(in oklab, var(--caution) 60%, transparent);
}

@media (max-width: 60rem) {
  .site-header .wrap { flex-direction: column; align-items: stretch; }
  .search-wrap { margin: 0; max-width: none; }
  nav { gap: 0.25rem 1rem; }
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    transition-duration: 0.01ms !important;
  }
  .paper-card, .paper-card:hover { transform: none; }
}
`
