package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var crossrefQueries = []string{
	"encrypted internet traffic classification",
	"encrypted traffic application identification",
	"VPN traffic fingerprinting",
	"TLS traffic analysis",
}

const (
	crossrefPageSize = 100
	crossrefPages    = 2
	// crossrefUA carries a mailto so Crossref routes us to its "polite"
	// pool. Without it these back-to-back title searches land in the
	// shared public pool, which 429s this request pattern within a
	// handful of requests — observed 2 runs in 3 dying on the 3rd
	// request, which silently cost us a whole query's results.
	crossrefUA = "circumvention-corpus-crawl/0.1 (https://github.com/getlantern/circumvention-corpus; mailto:a@lantern.io)"
	// crossrefDelay paces requests; the polite pool is generous but not
	// unmetered, and 8 requests back-to-back is what got us throttled.
	crossrefDelay = time.Second
	// crossrefRetries is how many times a single throttled page is
	// retried before we give up on that query and move to the next one.
	crossrefRetries = 3
	// crossrefMaxBackoff caps the exponential backoff used when Crossref
	// throttles us without a usable Retry-After.
	crossrefMaxBackoff = 30 * time.Second
	// crossrefMaxWait is the longest Retry-After we will actually sit out.
	// Beyond it the query fails and is reported rather than retried early:
	// retrying before the server-requested delay is what gets a client
	// blocked, and the caller already continues with the other queries.
	crossrefMaxWait = 2 * time.Minute
)

// bulkSources are high-volume metadata feeds that must never crowd
// curated candidates out of the classifier budget. See capClassifyBudget.
var bulkSources = map[string]bool{"crossref": true}

// crossrefSleep is a variable so tests don't pay the pacing delay.
var crossrefSleep = func(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func fetchCrossref(ctx context.Context, since time.Time) ([]candidate, error) {
	return fetchCrossrefFrom(ctx, &http.Client{Timeout: httpTimeout}, "https://api.crossref.org/works", since, crossrefQueries)
}

type crossrefWork struct {
	DOI      string   `json:"DOI"`
	Title    []string `json:"title"`
	Abstract string   `json:"abstract"`
	Venue    []string `json:"container-title"`
	Authors  []struct {
		Given  string `json:"given"`
		Family string `json:"family"`
		Name   string `json:"name"`
	} `json:"author"`
	Published struct {
		Parts [][]int `json:"date-parts"`
	} `json:"published"`
}

type crossrefBody struct {
	Message struct {
		Total int            `json:"total-results"`
		Items []crossrefWork `json:"items"`
	} `json:"message"`
}

func fetchCrossrefFrom(ctx context.Context, client *http.Client, endpoint string, since time.Time, queries []string) ([]candidate, error) {
	var out []candidate
	var errs []error
	seen := map[string]bool{}
	paced := false
	for _, query := range queries {
		for page := 0; page < crossrefPages; page++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			if paced {
				if err := crossrefSleep(ctx, crossrefDelay); err != nil {
					return out, err
				}
			}
			paced = true
			q := url.Values{
				"query.title": {query}, "rows": {strconv.Itoa(crossrefPageSize)},
				"offset": {strconv.Itoa(page * crossrefPageSize)},
				// from-index-date, not from-pub-date: Crossref maps a
				// date-parts of [[2026,9]] to 2026-09-01 and [[2026]] to
				// 2026-01-01, so a pub-date floor a few days back excludes
				// every month- or year-granularity record — which is most
				// conference proceedings, i.e. exactly the IEEE/ACM
				// metadata this source exists to reach. Indexing date is
				// the incremental-harvest filter: "what Crossref learned
				// about during the window", independent of how precisely
				// the publisher dated it. No until- filter either; journal
				// issues are routinely dated into the future.
				"filter": {"from-index-date:" + since.UTC().Format("2006-01-02")},
				"select": {"DOI,title,abstract,author,published,container-title"},
			}

			body, err := crossrefGet(ctx, client, endpoint, q)
			if err != nil {
				// A throttled or failing query must not take the remaining
				// queries down with it — each query reaches a different
				// slice of the literature (the second FlowPic paper is only
				// reachable via query 2), so aborting the source here loses
				// results that nothing else will surface.
				errs = append(errs, fmt.Errorf("%q: %w", query, err))
				break
			}
			for _, w := range body.Message.Items {
				doi := strings.ToLower(strings.TrimSpace(w.DOI))
				if doi == "" || seen[doi] || len(w.Title) == 0 {
					continue
				}
				c := candidate{Source: "crossref", Title: plainCrossrefText(w.Title[0]), Abstract: plainCrossrefText(w.Abstract), URL: "https://doi.org/" + doi, Refs: []string{"doi:" + doi}}
				if len(w.Published.Parts) > 0 && len(w.Published.Parts[0]) > 0 {
					c.Year = w.Published.Parts[0][0]
				}
				if len(w.Venue) > 0 {
					c.Venue = plainCrossrefText(w.Venue[0])
				}
				for _, a := range w.Authors {
					name := strings.TrimSpace(a.Given + " " + a.Family)
					if name == "" {
						name = a.Name
					}
					if name != "" {
						c.Authors = append(c.Authors, name)
					}
				}
				seen[doi] = true
				out = append(out, c)
			}
			if len(body.Message.Items) < crossrefPageSize || (page+1)*crossrefPageSize >= body.Message.Total {
				break
			}
			if page == crossrefPages-1 {
				log.Printf("crossref: %q capped at %d of %d ranked results; historical coverage is partial", query, crossrefPages*crossrefPageSize, body.Message.Total)
			}
		}
	}
	return out, errors.Join(errs...)
}

// crossrefGet fetches one page, retrying while Crossref is throttling us.
func crossrefGet(ctx context.Context, client *http.Client, endpoint string, q url.Values) (crossrefBody, error) {
	for attempt := 0; ; attempt++ {
		body, wait, err := crossrefPage(ctx, client, endpoint, q)
		if wait < 0 || attempt >= crossrefRetries {
			return body, err
		}
		switch {
		case wait == 0:
			// No usable Retry-After: back off exponentially instead.
			wait = min(time.Duration(1<<attempt)*time.Second, crossrefMaxBackoff)
		case wait > crossrefMaxWait:
			// Honor the delay or give up — never retry sooner than asked.
			return body, fmt.Errorf("%w (Retry-After %s exceeds %s)", err, wait, crossrefMaxWait)
		}
		log.Printf("crossref: rate limited on %q, retrying in %s", q.Get("query.title"), wait)
		if err := crossrefSleep(ctx, wait); err != nil {
			return crossrefBody{}, err
		}
	}
}

// crossrefPage returns the decoded page, or the Retry-After delay Crossref
// asked for when it throttled us. The delay is negative when the response
// was not a 429 and so must not be retried.
func crossrefPage(ctx context.Context, client *http.Client, endpoint string, q url.Values) (crossrefBody, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return crossrefBody{}, -1, err
	}
	req.Header.Set("User-Agent", crossrefUA)
	resp, err := client.Do(req)
	if err != nil {
		return crossrefBody{}, -1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		wait := time.Duration(-1)
		if resp.StatusCode == http.StatusTooManyRequests {
			wait = parseRetryAfter(resp.Header.Get("Retry-After"))
		}
		return crossrefBody{}, wait, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var body crossrefBody
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return crossrefBody{}, -1, err
	}
	return body, -1, nil
}

// parseRetryAfter reads the delay-seconds or HTTP-date form of the header.
// Returns 0 when the header is absent or unusable, leaving the choice of
// backoff to the caller.
func parseRetryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(h); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

var crossrefMarkup = regexp.MustCompile(`<[^>]*>`)

func plainCrossrefText(s string) string {
	return cleanText(html.UnescapeString(crossrefMarkup.ReplaceAllString(s, " ")))
}

// interleaveSources prevents a large source from consuming the entire classifier budget.
func interleaveSources(in []candidate) []candidate {
	order := []string{}
	groups := map[string][]candidate{}
	for _, c := range in {
		if _, ok := groups[c.Source]; !ok {
			order = append(order, c.Source)
		}
		groups[c.Source] = append(groups[c.Source], c)
	}
	out := make([]candidate, 0, len(in))
	for i := 0; len(out) < len(in); i++ {
		for _, source := range order {
			if i < len(groups[source]) {
				out = append(out, groups[source][i])
			}
		}
	}
	return out
}

// capClassifyBudget trims classifier input to budget without letting a bulk
// metadata feed evict curated candidates. Interleaving alone is not enough:
// Crossref matches hundreds of thousands of works per query, so once the
// low-volume groups are exhausted it takes every remaining round-robin slot
// and pushes net4people / gfw-report / foci candidates past the cap. A
// candidate dropped there is neither ingested nor rejection-cached, so when
// next week's 10-day window has moved past it, it is lost for good.
// Curated candidates therefore fill the budget first, bulk sources take
// what is left, and interleaved order is preserved within each tier.
func capClassifyBudget(in []candidate, budget int) []candidate {
	if budget <= 0 || len(in) <= budget {
		return in
	}
	out := make([]candidate, 0, budget)
	for _, c := range in {
		if !bulkSources[c.Source] && len(out) < budget {
			out = append(out, c)
		}
	}
	for _, c := range in {
		if len(out) >= budget {
			break
		}
		if bulkSources[c.Source] {
			out = append(out, c)
		}
	}
	return out
}
