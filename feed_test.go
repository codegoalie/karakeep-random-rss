package main

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func testConfig() *Config {
	return &Config{
		KarakeepURL: "https://karakeep.example.com",
		FeedTitle:   "Karakeep: a random link",
		FeedDesc:    "One random bookmark",
		PublicURL:   "https://feeds.example.com/feed.xml",
		Interval:    24 * time.Hour,
		FeedItems:   50,
	}
}

func TestRenderFeedIsWellFormedAndNewestFirst(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	items := []Item{
		{GUID: "g-old", BookmarkID: "b1", Title: "Older", URL: "https://example.com/1",
			PublishedAt: now.Add(-48 * time.Hour)},
		{GUID: "g-new", BookmarkID: "b2", Title: "Newer & shinier", URL: "https://example.com/2?a=1&b=2",
			Description: "A <script>alert(1)</script> description", Tags: []string{"go"},
			PublishedAt: now.Add(-24 * time.Hour)},
	}

	body, err := RenderFeed(testConfig(), items, now)
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Channel struct {
			Title string `xml:"title"`
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				GUID        string `xml:"guid"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("feed is not well-formed XML: %v", err)
	}

	if len(parsed.Channel.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(parsed.Channel.Items))
	}
	if parsed.Channel.Items[0].GUID != "g-new" {
		t.Fatalf("expected newest item first, got %q", parsed.Channel.Items[0].GUID)
	}
	if parsed.Channel.Items[0].Link != "https://example.com/2?a=1&b=2" {
		t.Fatalf("ampersand in link did not round-trip: %q", parsed.Channel.Items[0].Link)
	}
	if strings.Contains(parsed.Channel.Items[0].Description, "<script>") {
		t.Fatal("description HTML was not escaped")
	}

	raw := string(body)
	for _, want := range []string{
		`xmlns:atom="http://www.w3.org/2005/Atom"`,
		`<atom:link href="https://feeds.example.com/feed.xml" rel="self"`,
		`isPermaLink="false"`,
		`<ttl>1440</ttl>`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("feed is missing %s", want)
		}
	}
}

func TestRenderFeedAppliesItemTitlePrefix(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	items := []Item{
		{GUID: "g1", BookmarkID: "b1", Title: "Some Article", URL: "https://example.com/1", PublishedAt: now},
	}

	cfg := testConfig()
	cfg.ItemTitlePrefix = "🔖 From the stacks: "

	body, err := RenderFeed(cfg, items, now)
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Channel struct {
			Items []struct {
				Title string `xml:"title"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("feed is not well-formed XML: %v", err)
	}
	if len(parsed.Channel.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(parsed.Channel.Items))
	}
	want := "🔖 From the stacks: Some Article"
	if parsed.Channel.Items[0].Title != want {
		t.Fatalf("got title %q, want %q", parsed.Channel.Items[0].Title, want)
	}
	// The stored item must stay unprefixed; only the rendered feed changes.
	if items[0].Title != "Some Article" {
		t.Fatalf("stored item title was mutated: %q", items[0].Title)
	}
}

func TestRenderFeedEmptyItemTitlePrefixLeavesTitleUnchanged(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	items := []Item{
		{GUID: "g1", BookmarkID: "b1", Title: "Some Article", URL: "https://example.com/1", PublishedAt: now},
	}

	cfg := testConfig()
	cfg.ItemTitlePrefix = ""

	body, err := RenderFeed(cfg, items, now)
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Channel struct {
			Items []struct {
				Title string `xml:"title"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("feed is not well-formed XML: %v", err)
	}
	if parsed.Channel.Items[0].Title != "Some Article" {
		t.Fatalf("got title %q, want unchanged %q", parsed.Channel.Items[0].Title, "Some Article")
	}
}

func TestRenderFeedEmpty(t *testing.T) {
	body, err := RenderFeed(testConfig(), nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(body, new(struct{})); err != nil {
		t.Fatalf("empty feed is not well-formed: %v", err)
	}
}

func TestBuildItemTitleFallback(t *testing.T) {
	cases := []struct {
		name string
		b    Bookmark
		want string
	}{
		{"user title wins", Bookmark{Title: "Mine", Content: Content{Title: "Theirs", URL: "https://a.co/x"}}, "Mine"},
		{"falls back to page title", Bookmark{Content: Content{Title: "Theirs", URL: "https://a.co/x"}}, "Theirs"},
		{"falls back to host", Bookmark{Content: Content{URL: "https://a.co/x"}}, "a.co"},
		{"whitespace title is ignored", Bookmark{Title: "   ", Content: Content{URL: "https://a.co/x"}}, "a.co"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.b.DisplayTitle(); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestImageTypeGuess(t *testing.T) {
	cases := map[string]string{
		"https://a.co/i.png":      "image/png",
		"https://a.co/i.JPG?x=1":  "image/jpeg",
		"https://a.co/i.webp":     "image/webp",
		"https://a.co/no-ext":     "",
		"":                        "",
		"https://a.co/thing.html": "",
	}
	for in, want := range cases {
		if got := imageType(in); got != want {
			t.Errorf("imageType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFlexTimeParsesAndTolerates(t *testing.T) {
	var c Content
	raw := `{"type":"link","url":"https://a.co","datePublished":"2026-01-02T03:04:05.000Z"}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	if c.DatePublished.Year() != 2026 {
		t.Fatalf("expected 2026, got %v", c.DatePublished.Time)
	}

	// Garbage and null must not fail the decode of the whole bookmark.
	for _, bad := range []string{`{"datePublished":null}`, `{"datePublished":"not a date"}`, `{"datePublished":""}`} {
		var c2 Content
		if err := json.Unmarshal([]byte(bad), &c2); err != nil {
			t.Fatalf("%s should decode cleanly, got %v", bad, err)
		}
		if !c2.DatePublished.IsZero() {
			t.Fatalf("%s should leave a zero time", bad)
		}
	}
}
