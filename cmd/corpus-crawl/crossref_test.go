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
		if q.Get("query.title") != "encrypted traffic" || !strings.Contains(q.Get("filter"), "from-pub-date:2010-01-01") {
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

func TestCrossrefStopsOnRateLimit(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusTooManyRequests) }))
	defer server.Close()
	_, err := fetchCrossrefFrom(context.Background(), server.Client(), server.URL, time.Now(), []string{"one", "two"})
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
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
	for i, c := range got {
		if c.Title != want[i] {
			t.Fatalf("got %+v", got)
		}
	}
	if len(interleaveSources(nil)) != 0 {
		t.Fatal("nonempty output")
	}
}
