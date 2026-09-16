package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTrafficClassificationTitles(t *testing.T) {
	for _, title := range []string{
		"FlowPic: Encrypted Internet Traffic Classification is as Easy as Image Recognition",
		"FlowPic: A Generic Representation for Encrypted Traffic Classification and Applications Identification",
		"Encrypted Network Traffic Classification Using Deep Learning",
	} {
		if !matchesKeywordsInText(title) {
			t.Errorf("dropped %q", title)
		}
	}
	for _, title := range []string{"Road Traffic Classification with Image Recognition", "Encrypted Database Query Optimization"} {
		if matchesKeywordsInText(title) {
			t.Errorf("accepted %q", title)
		}
	}
	if !strings.Contains(arxivCategories, "cat:cs.NI") || !strings.Contains(arxivCategories, "cat:cs.CR") {
		t.Fatal("arxiv must include security and networking")
	}
}

func TestCrossrefPaginationMetadataAndDedup(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Get("query.title") != "encrypted traffic" || !strings.Contains(q.Get("filter"), "from-index-date:2010-01-01") {
			t.Errorf("wrong query: %v", q)
		}
		items := []map[string]any{}
		if q.Get("offset") == "0" {
			for i := 0; i < 100; i++ {
				items = append(items, map[string]any{"DOI": fmt.Sprintf("10.1/%d", i), "title": []string{"unrelated mathematics"}})
			}
		} else if q.Get("offset") == "100" {
			for i := 0; i < 2; i++ {
				items = append(items, map[string]any{"DOI": "10.1109/INFCOMW.2019.8845315", "title": []string{"FlowPic: Encrypted Internet Traffic Classification is as Easy as Image Recognition"}, "abstract": "<jats:p>Packets &amp; timing</jats:p>", "author": []map[string]string{{"given": "Tal", "family": "Shapira"}}, "container-title": []string{"INFOCOM Workshops"}, "published": map[string]any{"date-parts": [][]int{{2019, 4}}}})
			}
		} else {
			t.Errorf("unexpected offset %s", q.Get("offset"))
		}
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"total-results": 102, "items": items}})
	}))
	defer server.Close()
	out, err := fetchCrossrefFrom(context.Background(), server.Client(), server.URL, time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC), []string{"encrypted traffic"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(out) != 101 {
		t.Fatalf("calls=%d candidates=%d", calls, len(out))
	}
	c := out[100]
	if c.Year != 2019 || c.Abstract != "Packets & timing" || c.Authors[0] != "Tal Shapira" || c.URL != "https://doi.org/10.1109/infcomw.2019.8845315" {
		t.Fatalf("bad metadata: %+v", c)
	}
}

// noCrossrefSleep removes pacing and backoff delays, recording what the
// fetcher asked to wait for.
func noCrossrefSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	waits := &[]time.Duration{}
	prev := crossrefSleep
	crossrefSleep = func(ctx context.Context, d time.Duration) error {
		*waits = append(*waits, d)
		return ctx.Err()
	}
	t.Cleanup(func() { crossrefSleep = prev })
	return waits
}

// A throttled query must not take the remaining queries down with it: each
// query reaches a different slice of the literature, so aborting the source
// on one 429 silently loses results nothing else will surface.
func TestCrossrefRateLimitSkipsToNextQuery(t *testing.T) {
	waits := noCrossrefSleep(t)
	queries := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query.title")
		queries[query]++
		if query == "one" {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"total-results": 1, "items": []map[string]any{
			{"DOI": "10.1/x", "title": []string{"TLS traffic analysis at scale"}},
		}}})
	}))
	defer server.Close()
	out, err := fetchCrossrefFrom(context.Background(), server.Client(), server.URL, time.Now(), []string{"one", "two"})
	if err == nil {
		t.Fatal("throttled query should be reported")
	}
	if queries["one"] != crossrefRetries+1 {
		t.Errorf("retried %d times, want %d", queries["one"]-1, crossrefRetries)
	}
	if queries["two"] != 1 || len(out) != 1 {
		t.Fatalf("second query not reached: calls=%d candidates=%d", queries["two"], len(out))
	}
	for _, d := range (*waits)[:crossrefRetries] {
		if d != 7*time.Second && d != crossrefDelay {
			t.Errorf("ignored Retry-After: waited %s", d)
		}
	}
}

// A 429 that clears on retry must still return the page.
func TestCrossrefRetriesThroughRateLimit(t *testing.T) {
	noCrossrefSleep(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"total-results": 1, "items": []map[string]any{
			{"DOI": "10.1109/INFCOMW.2019.8845315", "title": []string{"FlowPic: Encrypted Internet Traffic Classification is as Easy as Image Recognition"}},
		}}})
	}))
	defer server.Close()
	out, err := fetchCrossrefFrom(context.Background(), server.Client(), server.URL, time.Now(), []string{"one"})
	if err != nil || len(out) != 1 {
		t.Fatalf("err=%v candidates=%d", err, len(out))
	}
}

// A non-429 failure is not retried.
func TestCrossrefDoesNotRetryServerError(t *testing.T) {
	noCrossrefSleep(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	_, err := fetchCrossrefFrom(context.Background(), server.Client(), server.URL, time.Now(), []string{"one"})
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("12"); got != 12*time.Second {
		t.Errorf("seconds form: %s", got)
	}
	if got := parseRetryAfter(time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)); got < 80*time.Second || got > 90*time.Second {
		t.Errorf("http-date form: %s", got)
	}
	for _, h := range []string{"", "soon", "-3", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)} {
		if got := parseRetryAfter(h); got != 0 {
			t.Errorf("%q: got %s, want 0 so the caller backs off", h, got)
		}
	}
}

func TestCrossrefCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fetchCrossrefFrom(ctx, http.DefaultClient, "http://unused.invalid", time.Now(), []string{"one"})
	if err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}

func TestInterleaveSources(t *testing.T) {
	in := []candidate{{Source: "arxiv", Title: "a1"}, {Source: "arxiv", Title: "a2"}, {Source: "arxiv", Title: "a3"}, {Source: "crossref", Title: "c1"}, {Source: "foci", Title: "f1"}, {Source: "crossref", Title: "c2"}}
	got := interleaveSources(in)
	want := []string{"a1", "c1", "f1", "a2", "c2", "a3"}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d: %+v", len(got), len(want), got)
	}
	for i, c := range got {
		if c.Title != want[i] {
			t.Fatalf("got %+v", got)
		}
	}
	if len(interleaveSources(nil)) != 0 {
		t.Fatal("nonempty output")
	}
}

// Crossref volume must not push curated candidates past --max-classify: a
// dropped candidate is neither ingested nor rejection-cached, so once the
// window moves past it, it is gone.
func TestCapClassifyBudgetProtectsCuratedSources(t *testing.T) {
	in := []candidate{{Source: "crossref", Title: "c1"}, {Source: "foci", Title: "f1"}, {Source: "crossref", Title: "c2"}, {Source: "net4people", Title: "n1"}, {Source: "crossref", Title: "c3"}}
	got := capClassifyBudget(in, 3)
	want := []string{"f1", "n1", "c1"}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d: %+v", len(got), len(want), got)
	}
	for i, c := range got {
		if c.Title != want[i] {
			t.Fatalf("got %+v, want %v", got, want)
		}
	}
	if len(capClassifyBudget(in, 0)) != len(in) || len(capClassifyBudget(in, 99)) != len(in) {
		t.Fatal("budget at or above input size must not trim")
	}
	// Curated candidates alone can exceed the budget.
	curated := []candidate{{Source: "foci", Title: "f1"}, {Source: "foci", Title: "f2"}, {Source: "crossref", Title: "c1"}}
	if got := capClassifyBudget(curated, 1); len(got) != 1 || got[0].Title != "f1" {
		t.Fatalf("got %+v", got)
	}
}
