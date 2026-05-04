// corpus-mcp is the MCP server for the circumvention research corpus.
//
// It exposes the corpus over stdio as an MCP server, with the tools
// documented in the project README. v0 reads YAML files from disk at
// startup and serves from in-memory indexes; that's sufficient for a
// few hundred papers and avoids any operational burden for the seed
// deployment. A Postgres-backed implementation will become worthwhile
// once the corpus crosses ~1000 papers or full-text search becomes a
// bottleneck.
//
// Usage:
//
//	corpus-mcp --corpus /path/to/circumvention-corpus
//
// Pass --visibility=public to expose only public records (used by the
// public-facing endpoint). The default lets all visibility levels
// through, which is what the local Lantern-team Claude Code instances
// want.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	serverName    = "circumvention-corpus"
	serverVersion = "0.1.0"
	protocolRev   = "2024-11-05"
)

// Paper mirrors schema/paper.schema.json. Only the fields the MCP layer
// reads are typed; unknown fields are ignored on load (extra fields can
// be added to YAMLs without rebuilding the server).
type Paper struct {
	ID                       string   `yaml:"id" json:"id"`
	Title                    string   `yaml:"title" json:"title"`
	Authors                  []string `yaml:"authors,omitempty" json:"authors,omitempty"`
	Venue                    string   `yaml:"venue,omitempty" json:"venue,omitempty"`
	Year                     int      `yaml:"year" json:"year"`
	DOI                      string   `yaml:"doi,omitempty" json:"doi,omitempty"`
	ArxivID                  string   `yaml:"arxiv_id,omitempty" json:"arxiv_id,omitempty"`
	URL                      string   `yaml:"url,omitempty" json:"url,omitempty"`
	Abstract                 string   `yaml:"abstract,omitempty" json:"abstract,omitempty"`
	Censors                  []string `yaml:"censors" json:"censors"`
	Techniques               []string `yaml:"techniques" json:"techniques"`
	DefensesDiscussed        []string `yaml:"defenses_discussed,omitempty" json:"defenses_discussed,omitempty"`
	DefensesEvaluatedAgainst []string `yaml:"defenses_evaluated_against,omitempty" json:"defenses_evaluated_against,omitempty"`
	EvaluationMethods        []string `yaml:"evaluation_methods,omitempty" json:"evaluation_methods,omitempty"`
	Core                     bool     `yaml:"core,omitempty" json:"core,omitempty"`
	Notes                    string   `yaml:"notes,omitempty" json:"notes,omitempty"`
	Visibility               string   `yaml:"visibility" json:"visibility"`
	EmbargoUntil             string   `yaml:"embargo_until,omitempty" json:"embargo_until,omitempty"`
	RedistributionTerms      string   `yaml:"redistribution_terms,omitempty" json:"redistribution_terms,omitempty"`
	SharedBy                 string   `yaml:"shared_by,omitempty" json:"shared_by,omitempty"`
	SharedOn                 string   `yaml:"shared_on,omitempty" json:"shared_on,omitempty"`
	DateAdded                string   `yaml:"date_added,omitempty" json:"date_added,omitempty"`
	AddedBy                  string   `yaml:"added_by,omitempty" json:"added_by,omitempty"`
	Sources                  []string `yaml:"sources,omitempty" json:"sources,omitempty"`
	References               []string `yaml:"references,omitempty" json:"references,omitempty"`
}

// store holds the loaded corpus. Searches run over the in-memory slice;
// the corpus is small enough that a linear scan is fine well past v0.
type store struct {
	mu     sync.RWMutex
	papers []*Paper
	byID   map[string]*Paper
	// taxonomy is loaded raw and passed through to list_taxonomy callers
	// without parsing — clients want the whole document, not individual fields.
	taxonomy map[string]any
	// publicOnly clamps every read to visibility=public (the public-facing
	// endpoint). When false, all visibility tiers are returned.
	publicOnly bool
}

func loadStore(corpusDir string, publicOnly bool) (*store, error) {
	s := &store{byID: map[string]*Paper{}, publicOnly: publicOnly}

	papersDir := filepath.Join(corpusDir, "corpus", "papers")
	entries, err := os.ReadDir(papersDir)
	if err != nil {
		return nil, fmt.Errorf("read papers dir %s: %w", papersDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(papersDir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var p Paper
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if p.ID == "" {
			return nil, fmt.Errorf("%s: missing id", path)
		}
		if _, dup := s.byID[p.ID]; dup {
			return nil, fmt.Errorf("%s: duplicate paper id %s", path, p.ID)
		}
		s.byID[p.ID] = &p
		s.papers = append(s.papers, &p)
	}

	// Load taxonomy as raw map for list_taxonomy.
	taxPath := filepath.Join(corpusDir, "schema", "taxonomy.yaml")
	taxRaw, err := os.ReadFile(taxPath)
	if err != nil {
		return nil, fmt.Errorf("read taxonomy: %w", err)
	}
	if err := yaml.Unmarshal(taxRaw, &s.taxonomy); err != nil {
		return nil, fmt.Errorf("parse taxonomy: %w", err)
	}

	return s, nil
}

// visible returns true if this paper should be served given the current
// visibility clamp. The public-facing endpoint runs with publicOnly=true,
// so non-public records are never even visited; the local instance runs
// with publicOnly=false and serves everything.
func (s *store) visible(p *Paper) bool {
	if !s.publicOnly {
		return true
	}
	return p.Visibility == "public"
}

// search executes a simple keyword search over title / abstract / notes
// plus exact-match filters on tag fields. Ranking: papers matching all
// filters first, then by core==true, then by year desc.
func (s *store) search(query string, filters searchFilters, limit int) []*Paper {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 20
	}
	q := strings.ToLower(strings.TrimSpace(query))

	var matches []*Paper
	for _, p := range s.papers {
		if !s.visible(p) {
			continue
		}
		if !filters.match(p) {
			continue
		}
		if q != "" && !textMatch(p, q) {
			continue
		}
		matches = append(matches, p)
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Core != matches[j].Core {
			return matches[i].Core
		}
		return matches[i].Year > matches[j].Year
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func textMatch(p *Paper, q string) bool {
	if strings.Contains(strings.ToLower(p.Title), q) {
		return true
	}
	if strings.Contains(strings.ToLower(p.Abstract), q) {
		return true
	}
	if strings.Contains(strings.ToLower(p.Notes), q) {
		return true
	}
	for _, a := range p.Authors {
		if strings.Contains(strings.ToLower(a), q) {
			return true
		}
	}
	return false
}

type searchFilters struct {
	Censors    []string
	Techniques []string
	Defenses   []string
	YearMin    int
	YearMax    int
	Venue      string
	CoreOnly   bool
}

func (f searchFilters) match(p *Paper) bool {
	if len(f.Censors) > 0 && !anyOverlap(f.Censors, p.Censors) {
		return false
	}
	if len(f.Techniques) > 0 && !anyOverlap(f.Techniques, p.Techniques) {
		return false
	}
	if len(f.Defenses) > 0 {
		all := append(append([]string{}, p.DefensesDiscussed...), p.DefensesEvaluatedAgainst...)
		if !anyOverlap(f.Defenses, all) {
			return false
		}
	}
	if f.YearMin > 0 && p.Year < f.YearMin {
		return false
	}
	if f.YearMax > 0 && p.Year > f.YearMax {
		return false
	}
	if f.Venue != "" && !strings.EqualFold(f.Venue, p.Venue) {
		return false
	}
	if f.CoreOnly && !p.Core {
		return false
	}
	return true
}

func anyOverlap(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

func (s *store) get(id string) (*Paper, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[id]
	if !ok || !s.visible(p) {
		return nil, false
	}
	return p, true
}

// findRelated returns papers that share at least one tag in the given
// dimension. mode: "same_technique", "same_censor", "same_defense".
func (s *store) findRelated(id, mode string, limit int) []*Paper {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src, ok := s.byID[id]
	if !ok || !s.visible(src) {
		return nil
	}
	if limit <= 0 {
		limit = 10
	}
	var pool []string
	switch mode {
	case "same_technique", "":
		pool = src.Techniques
	case "same_censor":
		pool = src.Censors
	case "same_defense":
		pool = append(append([]string{}, src.DefensesDiscussed...), src.DefensesEvaluatedAgainst...)
	default:
		return nil
	}
	type scored struct {
		p     *Paper
		score int
	}
	var out []scored
	for _, p := range s.papers {
		if p.ID == id || !s.visible(p) {
			continue
		}
		var bag []string
		switch mode {
		case "same_technique", "":
			bag = p.Techniques
		case "same_censor":
			bag = p.Censors
		case "same_defense":
			bag = append(append([]string{}, p.DefensesDiscussed...), p.DefensesEvaluatedAgainst...)
		}
		score := 0
		for _, t := range pool {
			if slices.Contains(bag, t) {
				score++
			}
		}
		if score > 0 {
			out = append(out, scored{p, score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		if out[i].p.Core != out[j].p.Core {
			return out[i].p.Core
		}
		return out[i].p.Year > out[j].p.Year
	})
	if len(out) > limit {
		out = out[:limit]
	}
	res := make([]*Paper, len(out))
	for i, s := range out {
		res[i] = s.p
	}
	return res
}

// JSON-RPC envelope (subset of the spec we use here).
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

var tools = []tool{
	{
		Name: "search_papers",
		Description: "Full-text + tag-filter search over the corpus. Returns ranked paper records with id, title, year, venue, abstract, and tags. Use to find papers about a specific censor, technique, or defense.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":      map[string]any{"type": "string", "description": "Free-text search over title, abstract, authors, and team notes."},
				"censors":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter to papers covering any of these censors (taxonomy IDs, e.g. cn / ir / ru)."},
				"techniques": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter to papers covering any of these detection techniques."},
				"defenses":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter to papers covering any of these defenses."},
				"year_min":   map[string]any{"type": "integer"},
				"year_max":   map[string]any{"type": "integer"},
				"venue":      map[string]any{"type": "string"},
				"core_only":  map[string]any{"type": "boolean", "description": "If true, only return papers marked as 'core' (must-read)."},
				"limit":      map[string]any{"type": "integer", "default": 20},
			},
		},
	},
	{
		Name:        "get_paper",
		Description: "Return the full paper record for a single id.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"id"},
			"properties": map[string]any{
				"id": map[string]any{"type": "string"},
			},
		},
	},
	{
		Name:        "list_taxonomy",
		Description: "Return the controlled-vocabulary taxonomy (censors, techniques, defenses, etc.). Use this before issuing a search to discover the canonical IDs to filter on.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"category": map[string]any{
					"type":        "string",
					"description": "Optional: return only one section of the taxonomy. One of censors, techniques, defenses, evaluation_methods, visibility_levels.",
				},
			},
		},
	},
	{
		Name:        "find_related",
		Description: "Return papers that share tags with the given paper. mode = same_technique (default), same_censor, or same_defense.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"id"},
			"properties": map[string]any{
				"id":    map[string]any{"type": "string"},
				"mode":  map[string]any{"type": "string", "enum": []string{"same_technique", "same_censor", "same_defense"}},
				"limit": map[string]any{"type": "integer", "default": 10},
			},
		},
	},
}

func (s *store) handle(req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolRev,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
		}
	case "tools/list":
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: err.Error()}
			return resp
		}
		out, err := s.callTool(p.Name, p.Arguments)
		if err != nil {
			resp.Result = map[string]any{
				"isError": true,
				"content": []any{map[string]any{"type": "text", "text": err.Error()}},
			}
			return resp
		}
		resp.Result = map[string]any{
			"content": []any{map[string]any{"type": "text", "text": out}},
		}
	case "notifications/initialized":
		// MCP convention: this is a notification (no id). No response needed,
		// but our caller is built to swallow empty responses gracefully.
		return rpcResponse{}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

func (s *store) callTool(name string, raw json.RawMessage) (string, error) {
	switch name {
	case "search_papers":
		var args struct {
			Query      string   `json:"query"`
			Censors    []string `json:"censors"`
			Techniques []string `json:"techniques"`
			Defenses   []string `json:"defenses"`
			YearMin    int      `json:"year_min"`
			YearMax    int      `json:"year_max"`
			Venue      string   `json:"venue"`
			CoreOnly   bool     `json:"core_only"`
			Limit      int      `json:"limit"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		results := s.search(args.Query, searchFilters{
			Censors:    args.Censors,
			Techniques: args.Techniques,
			Defenses:   args.Defenses,
			YearMin:    args.YearMin,
			YearMax:    args.YearMax,
			Venue:      args.Venue,
			CoreOnly:   args.CoreOnly,
		}, args.Limit)
		return jsonString(map[string]any{"papers": results, "count": len(results)})
	case "get_paper":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		p, ok := s.get(args.ID)
		if !ok {
			return "", fmt.Errorf("paper not found: %s", args.ID)
		}
		return jsonString(p)
	case "list_taxonomy":
		var args struct {
			Category string `json:"category"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.Category == "" {
			return jsonString(s.taxonomy)
		}
		section, ok := s.taxonomy[args.Category]
		if !ok {
			return "", fmt.Errorf("unknown taxonomy category: %s", args.Category)
		}
		return jsonString(map[string]any{args.Category: section})
	case "find_related":
		var args struct {
			ID    string `json:"id"`
			Mode  string `json:"mode"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		results := s.findRelated(args.ID, args.Mode, args.Limit)
		return jsonString(map[string]any{"papers": results, "count": len(results)})
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func jsonString(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func main() {
	corpusDir := flag.String("corpus", ".", "Path to the circumvention-corpus repo root.")
	publicOnly := flag.Bool("public-only", false, "If true, serve only visibility=public records. Used by the public-facing endpoint.")
	flag.Parse()

	abs, err := filepath.Abs(*corpusDir)
	if err != nil {
		log.Fatal(err)
	}
	s, err := loadStore(abs, *publicOnly)
	if err != nil {
		log.Fatal(err)
	}
	visBanner := "all visibility tiers"
	if *publicOnly {
		visBanner = "public only"
	}
	log.Printf("corpus-mcp v%s loaded %d papers from %s (%s)", serverVersion, len(s.papers), abs, visBanner)

	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	enc := json.NewEncoder(out)

	for {
		line, err := in.ReadString('\n')
		if err == io.EOF {
			return
		}
		if err != nil {
			log.Printf("read: %v", err)
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			log.Printf("parse: %v", err)
			continue
		}
		resp := s.handle(req)
		// notifications/initialized produces a zero-value response we suppress.
		if resp.JSONRPC == "" {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			log.Printf("write: %v", err)
			return
		}
		if err := out.Flush(); err != nil {
			log.Printf("flush: %v", err)
			return
		}
	}
}
