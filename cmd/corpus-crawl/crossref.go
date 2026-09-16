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

const crossrefPageSize = 100
const crossrefPages = 2

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

func fetchCrossrefFrom(ctx context.Context, client *http.Client, endpoint string, since time.Time, queries []string) ([]candidate, error) {
	var out []candidate
	var errs []error
	seen := map[string]bool{}
	for _, query := range queries {
		for page := 0; page < crossrefPages; page++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			q := url.Values{
				"query.title": {query}, "rows": {strconv.Itoa(crossrefPageSize)},
				"offset": {strconv.Itoa(page * crossrefPageSize)},
				"filter": {"from-pub-date:" + since.UTC().Format("2006-01-02") + ",until-pub-date:" + time.Now().UTC().Format("2006-01-02")},
				"select": {"DOI,title,abstract,author,published,container-title"},
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
			if err != nil {
				return out, err
			}
			req.Header.Set("User-Agent", "circumvention-corpus-crawl/0.1 (https://github.com/getlantern/circumvention-corpus)")
			resp, err := client.Do(req)
			if err != nil {
				errs = append(errs, fmt.Errorf("%q: %w", query, err))
				break
			}
			var result struct {
				Message struct {
					Total int            `json:"total-results"`
					Items []crossrefWork `json:"items"`
				} `json:"message"`
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				errs = append(errs, fmt.Errorf("%q: HTTP %d", query, resp.StatusCode))
				if resp.StatusCode == http.StatusTooManyRequests {
					return out, errors.Join(errs...)
				}
				break
			}
			err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result)
			resp.Body.Close()
			if err != nil {
				errs = append(errs, fmt.Errorf("%q: %w", query, err))
				break
			}
			for _, w := range result.Message.Items {
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
			if len(result.Message.Items) < crossrefPageSize || (page+1)*crossrefPageSize >= result.Message.Total {
				break
			}
			if page == crossrefPages-1 {
				log.Printf("crossref: %q capped at %d of %d ranked results; historical coverage is partial", query, crossrefPages*crossrefPageSize, result.Message.Total)
			}
		}
	}
	return out, errors.Join(errs...)
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
