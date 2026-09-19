package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a minimal read-only client for the Karakeep REST API.
// Docs: https://docs.karakeep.app/api/karakeep-api/
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// flexTime parses the handful of timestamp shapes Karakeep can emit without
// failing the whole decode when it sees something unexpected or null.
type flexTime struct{ time.Time }

var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.000Z",
	"2006-01-02T15:04:05Z",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func (t *flexTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	for _, layout := range timeLayouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			t.Time = parsed
			return nil
		}
	}
	// Unknown shape: leave zero rather than breaking the run.
	return nil
}

type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Content struct {
	Type          string   `json:"type"` // "link", "text", "asset"
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	ImageURL      string   `json:"imageUrl"`
	Author        string   `json:"author"`
	Publisher     string   `json:"publisher"`
	DatePublished flexTime `json:"datePublished"`
}

type Bookmark struct {
	ID         string   `json:"id"`
	CreatedAt  flexTime `json:"createdAt"`
	Title      string   `json:"title"`
	Archived   bool     `json:"archived"`
	Favourited bool     `json:"favourited"`
	Note       string   `json:"note"`
	Summary    string   `json:"summary"`
	Tags       []Tag    `json:"tags"`
	Content    Content  `json:"content"`
}

// DisplayTitle picks the best human title available, falling back to the host.
func (b Bookmark) DisplayTitle() string {
	for _, candidate := range []string{b.Title, b.Content.Title} {
		if s := strings.TrimSpace(candidate); s != "" {
			return s
		}
	}
	if u, err := url.Parse(b.Content.URL); err == nil && u.Host != "" {
		return u.Host
	}
	if b.Content.URL != "" {
		return b.Content.URL
	}
	return "Untitled bookmark"
}

// HasTag reports whether the bookmark carries the named tag (case insensitive).
func (b Bookmark) HasTag(name string) bool {
	for _, t := range b.Tags {
		if strings.EqualFold(t.Name, name) {
			return true
		}
	}
	return false
}

type bookmarksResponse struct {
	Bookmarks  []Bookmark `json:"bookmarks"`
	NextCursor *string    `json:"nextCursor"`
}

type listsResponse struct {
	Lists []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"lists"`
}

func (c *Client) do(ctx context.Context, path string, query url.Values, out any) error {
	endpoint := c.BaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(snippet)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ListID resolves a list name to its ID. Returns "" if no list matches.
func (c *Client) ListID(ctx context.Context, name string) (string, error) {
	var out listsResponse
	if err := c.do(ctx, "/api/v1/lists", nil, &out); err != nil {
		return "", err
	}
	for _, l := range out.Lists {
		if strings.EqualFold(l.Name, name) {
			return l.ID, nil
		}
	}
	return "", nil
}

// FetchLinks walks every page of bookmarks and returns the non-archived link
// bookmarks, optionally restricted to a list and/or a tag.
func (c *Client) FetchLinks(ctx context.Context, listName, tag string, maxPages int) ([]Bookmark, error) {
	path := "/api/v1/bookmarks"
	if listName != "" {
		id, err := c.ListID(ctx, listName)
		if err != nil {
			return nil, fmt.Errorf("resolving list %q: %w", listName, err)
		}
		if id == "" {
			return nil, fmt.Errorf("no Karakeep list named %q", listName)
		}
		path = "/api/v1/lists/" + url.PathEscape(id) + "/bookmarks"
	}

	var (
		links  []Bookmark
		cursor string
		seen   = map[string]bool{}
	)

	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(100))
		q.Set("archived", "false")
		q.Set("includeContent", "false")
		if cursor != "" {
			q.Set("cursor", cursor)
		}

		var out bookmarksResponse
		if err := c.do(ctx, path, q, &out); err != nil {
			return nil, err
		}

		for _, b := range out.Bookmarks {
			// Defensive: honour archived even if the server ignored the filter,
			// and skip anything that is not a link with a usable URL.
			if b.Archived || b.Content.Type != "link" || strings.TrimSpace(b.Content.URL) == "" {
				continue
			}
			if tag != "" && !b.HasTag(tag) {
				continue
			}
			if seen[b.ID] {
				continue
			}
			seen[b.ID] = true
			links = append(links, b)
		}

		if out.NextCursor == nil || *out.NextCursor == "" || len(out.Bookmarks) == 0 {
			return links, nil
		}
		cursor = *out.NextCursor
	}

	return links, fmt.Errorf("stopped after %d pages; raise -max-pages if your library is larger", maxPages)
}
