package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeKarakeep serves a paginated bookmark library the way Karakeep does.
func fakeKarakeep(t *testing.T, total int, pageSize int) (*httptest.Server, *int) {
	t.Helper()
	calls := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/lists", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"lists": []map[string]string{{"id": "list-1", "name": "Read Later"}},
		})
	})
	handleBookmarks := func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"unauthorized"}`)
			return
		}
		calls++

		start := 0
		if c := r.URL.Query().Get("cursor"); c != "" {
			start, _ = strconv.Atoi(c)
		}
		end := start + pageSize
		if end > total {
			end = total
		}

		bookmarks := []map[string]any{}
		for i := start; i < end; i++ {
			bookmarks = append(bookmarks, map[string]any{
				"id":        fmt.Sprintf("bk-%03d", i),
				"createdAt": "2026-03-04T05:06:07.000Z",
				"title":     nil,
				"archived":  false,
				"tags":      []map[string]string{{"id": "t1", "name": "reading"}},
				"content": map[string]any{
					"type":        "link",
					"url":         fmt.Sprintf("https://example.com/post/%d", i),
					"title":       fmt.Sprintf("Post %d", i),
					"description": "A description with <b>html</b> & an ampersand",
					"imageUrl":    "https://example.com/cover.png",
					"publisher":   "Example",
				},
			})
		}
		// Mix in things that must be filtered out.
		if start == 0 {
			bookmarks = append(bookmarks,
				map[string]any{"id": "note-1", "archived": false,
					"content": map[string]any{"type": "text", "text": "just a note"}},
				map[string]any{"id": "arch-1", "archived": true,
					"content": map[string]any{"type": "link", "url": "https://example.com/archived"}},
				map[string]any{"id": "empty-1", "archived": false,
					"content": map[string]any{"type": "link", "url": ""}},
			)
		}

		resp := map[string]any{"bookmarks": bookmarks}
		if end < total {
			resp["nextCursor"] = strconv.Itoa(end)
		} else {
			resp["nextCursor"] = nil
		}
		json.NewEncoder(w).Encode(resp)
	}
	mux.HandleFunc("/api/v1/bookmarks", handleBookmarks)
	mux.HandleFunc("/api/v1/lists/list-1/bookmarks", handleBookmarks)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestFetchLinksPaginatesAndFilters(t *testing.T) {
	srv, calls := fakeKarakeep(t, 25, 10)
	c := NewClient(srv.URL, "test-key", 5*time.Second)

	links, err := c.FetchLinks(context.Background(), "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 25 {
		t.Fatalf("expected 25 link bookmarks, got %d", len(links))
	}
	if *calls != 3 {
		t.Fatalf("expected 3 pages, got %d", *calls)
	}
	for _, l := range links {
		if l.Archived || l.Content.Type != "link" || l.Content.URL == "" {
			t.Fatalf("filter let through %+v", l)
		}
	}
}

func TestFetchLinksTagFilter(t *testing.T) {
	srv, _ := fakeKarakeep(t, 5, 10)
	c := NewClient(srv.URL, "test-key", 5*time.Second)

	matching, err := c.FetchLinks(context.Background(), "", "reading", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matching) != 5 {
		t.Fatalf("expected all 5 to carry the tag, got %d", len(matching))
	}

	none, err := c.FetchLinks(context.Background(), "", "nonexistent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no matches, got %d", len(none))
	}
}

func TestFetchLinksByList(t *testing.T) {
	srv, _ := fakeKarakeep(t, 3, 10)
	c := NewClient(srv.URL, "test-key", 5*time.Second)

	links, err := c.FetchLinks(context.Background(), "read later", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 3 {
		t.Fatalf("expected 3, got %d", len(links))
	}

	if _, err := c.FetchLinks(context.Background(), "no such list", "", 10); err == nil {
		t.Fatal("expected an error for an unknown list")
	}
}

func TestFetchLinksBadKey(t *testing.T) {
	srv, _ := fakeKarakeep(t, 3, 10)
	c := NewClient(srv.URL, "wrong-key", 5*time.Second)
	if _, err := c.FetchLinks(context.Background(), "", "", 10); err == nil {
		t.Fatal("expected an auth error")
	}
}

// End to end: publish repeatedly against the fake server and check the feed.
func TestPublishEndToEnd(t *testing.T) {
	srv, _ := fakeKarakeep(t, 6, 4)
	statePath := filepath.Join(t.TempDir(), "state.json")

	cfg := &Config{
		KarakeepURL: srv.URL,
		APIKey:      "test-key",
		Interval:    time.Hour,
		FeedItems:   4,
		FeedTitle:   "Random",
		FeedDesc:    "desc",
		PublicURL:   "https://feeds.example.com/feed.xml",
		HTTPTimeout: 5 * time.Second,
		MaxPages:    10,
	}
	store, err := NewStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: cfg, store: store, client: NewClient(srv.URL, "test-key", 5*time.Second)}

	for i := 0; i < 6; i++ {
		if err := s.publish(context.Background()); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	st := store.Snapshot()
	if len(st.SeenIDs) != 6 {
		t.Fatalf("expected 6 seen, got %d", len(st.SeenIDs))
	}
	distinct := map[string]bool{}
	for _, id := range st.SeenIDs {
		distinct[id] = true
	}
	if len(distinct) != 6 {
		t.Fatalf("a link repeated within the first cycle: %v", st.SeenIDs)
	}
	if len(st.Items) != cfg.FeedItems {
		t.Fatalf("feed should be trimmed to %d, got %d", cfg.FeedItems, len(st.Items))
	}
	if st.NextPublishAt.IsZero() {
		t.Fatal("next publish time was not scheduled")
	}

	// The seventh publish must roll the cycle.
	if err := s.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Cycle; got != 1 {
		t.Fatalf("expected cycle 1 after exhausting the library, got %d", got)
	}

	// And the served feed must be valid, newest-first RSS.
	rec := httptest.NewRecorder()
	s.handleFeed(rec, httptest.NewRequest(http.MethodGet, "/feed.xml", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("feed returned %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/rss+xml") {
		t.Fatalf("unexpected content type %q", ct)
	}

	var parsed struct {
		Channel struct {
			Items []struct {
				Title   string `xml:"title"`
				Link    string `xml:"link"`
				PubDate string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("served feed is not well-formed: %v", err)
	}
	if len(parsed.Channel.Items) != cfg.FeedItems {
		t.Fatalf("expected %d items in the feed, got %d", cfg.FeedItems, len(parsed.Channel.Items))
	}
	for _, it := range parsed.Channel.Items {
		if !strings.HasPrefix(it.Link, "https://example.com/post/") {
			t.Fatalf("unexpected link %q", it.Link)
		}
		if _, err := time.Parse(time.RFC1123Z, it.PubDate); err != nil {
			t.Fatalf("pubDate %q is not RFC1123Z: %v", it.PubDate, err)
		}
	}
}

func TestConditionalGet(t *testing.T) {
	srv, _ := fakeKarakeep(t, 3, 10)
	store, _ := NewStore(filepath.Join(t.TempDir(), "state.json"))
	cfg := &Config{KarakeepURL: srv.URL, Interval: time.Hour, FeedItems: 10, HTTPTimeout: time.Second, MaxPages: 5}
	s := &Server{cfg: cfg, store: store, client: NewClient(srv.URL, "test-key", time.Second)}
	if err := s.publish(context.Background()); err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRecorder()
	s.handleFeed(first, httptest.NewRequest(http.MethodGet, "/feed.xml", nil))
	tag := first.Header().Get("ETag")
	if tag == "" {
		t.Fatal("expected an ETag")
	}

	req := httptest.NewRequest(http.MethodGet, "/feed.xml", nil)
	req.Header.Set("If-None-Match", tag)
	second := httptest.NewRecorder()
	s.handleFeed(second, req)
	if second.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", second.Code)
	}
}

func TestPublishEndpointRequiresToken(t *testing.T) {
	srv, _ := fakeKarakeep(t, 3, 10)
	store, _ := NewStore(filepath.Join(t.TempDir(), "state.json"))
	cfg := &Config{KarakeepURL: srv.URL, Interval: time.Hour, FeedItems: 10, HTTPTimeout: time.Second, MaxPages: 5}
	s := &Server{cfg: cfg, store: store, client: NewClient(srv.URL, "test-key", time.Second)}

	// Disabled by default.
	rec := httptest.NewRecorder()
	s.handlePublish(rec, httptest.NewRequest(http.MethodPost, "/publish", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when no admin token is set, got %d", rec.Code)
	}

	cfg.AdminToken = "s3cret"
	rec = httptest.NewRecorder()
	s.handlePublish(rec, httptest.NewRequest(http.MethodPost, "/publish", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without the token, got %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/publish", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec = httptest.NewRecorder()
	s.handlePublish(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with the token, got %d: %s", rec.Code, rec.Body)
	}
}

func TestLoadConfigValidation(t *testing.T) {
	t.Setenv("KARAKEEP_URL", "")
	t.Setenv("KARAKEEP_API_KEY", "")

	if _, err := LoadConfig([]string{"-api-key", "k"}); err == nil {
		t.Fatal("expected an error without a URL")
	}
	if _, err := LoadConfig([]string{"-karakeep-url", "https://k.example.com"}); err == nil {
		t.Fatal("expected an error without an API key")
	}
	if _, err := LoadConfig([]string{"-karakeep-url", "not-a-url", "-api-key", "k"}); err == nil {
		t.Fatal("expected an error for a relative URL")
	}
	if _, err := LoadConfig([]string{"-karakeep-url", "https://k.example.com", "-api-key", "k", "-interval", "10s"}); err == nil {
		t.Fatal("expected an error for an absurdly short interval")
	}

	cfg, err := LoadConfig([]string{"-karakeep-url", "https://k.example.com/", "-api-key", "k", "-interval", "8h"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KarakeepURL != "https://k.example.com" {
		t.Fatalf("trailing slash was not trimmed: %q", cfg.KarakeepURL)
	}
	if cfg.Interval != 8*time.Hour {
		t.Fatalf("interval not applied: %s", cfg.Interval)
	}
}
