package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"net/url"
	"path"
	"strings"
	"time"
)

type rss struct {
	XMLName   xml.Name `xml:"rss"`
	Version   string   `xml:"version,attr"`
	AtomNS    string   `xml:"xmlns:atom,attr"`
	ContentNS string   `xml:"xmlns:content,attr"`
	Channel   channel  `xml:"channel"`
}

type channel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Generator   string    `xml:"generator"`
	Language    string    `xml:"language,omitempty"`
	LastBuild   string    `xml:"lastBuildDate,omitempty"`
	TTL         int       `xml:"ttl,omitempty"`
	AtomLink    *atomLink `xml:"atom:link,omitempty"`
	Items       []rssItem `xml:"item"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type rssItem struct {
	Title       string     `xml:"title"`
	Link        string     `xml:"link"`
	GUID        guid       `xml:"guid"`
	PubDate     string     `xml:"pubDate"`
	Description string     `xml:"description"`
	Author      string     `xml:"author,omitempty"`
	Categories  []string   `xml:"category,omitempty"`
	Enclosure   *enclosure `xml:"enclosure,omitempty"`
	Source      string     `xml:"source,omitempty"`
}

type guid struct {
	Value       string `xml:",chardata"`
	IsPermaLink string `xml:"isPermaLink,attr"`
}

type enclosure struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
	Len  int    `xml:"length,attr"`
}

// MakeGUID builds a GUID that is stable for a given bookmark within a cycle but
// changes when the library wraps around, so a resurfaced link shows up as a new
// unread item in Miniflux instead of being deduplicated.
func MakeGUID(bookmarkID string, cycle int) string {
	return fmt.Sprintf("urn:karakeep:%s:cycle:%d", bookmarkID, cycle)
}

// BuildItem turns a Karakeep bookmark into a feed item.
func BuildItem(b Bookmark, cycle int, now time.Time) Item {
	tags := make([]string, 0, len(b.Tags))
	for _, t := range b.Tags {
		if name := strings.TrimSpace(t.Name); name != "" {
			tags = append(tags, name)
		}
	}
	return Item{
		GUID:        MakeGUID(b.ID, cycle),
		BookmarkID:  b.ID,
		Title:       b.DisplayTitle(),
		URL:         b.Content.URL,
		Description: strings.TrimSpace(b.Content.Description),
		Note:        strings.TrimSpace(b.Note),
		Summary:     strings.TrimSpace(b.Summary),
		ImageURL:    strings.TrimSpace(b.Content.ImageURL),
		Author:      strings.TrimSpace(b.Content.Author),
		Publisher:   strings.TrimSpace(b.Content.Publisher),
		Tags:        tags,
		SavedAt:     b.CreatedAt.Time,
		PublishedAt: now,
	}
}

// renderDescription assembles the HTML body Miniflux will show.
func renderDescription(it Item) string {
	var b strings.Builder

	if it.ImageURL != "" {
		fmt.Fprintf(&b, `<p><img src="%s" alt="" /></p>`, html.EscapeString(it.ImageURL))
	}
	if it.Summary != "" {
		fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(it.Summary))
	} else if it.Description != "" {
		fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(it.Description))
	}
	if it.Note != "" {
		fmt.Fprintf(&b, "<blockquote><p><strong>My note:</strong> %s</p></blockquote>",
			html.EscapeString(it.Note))
	}

	fmt.Fprintf(&b, `<p><a href="%s">%s</a></p>`,
		html.EscapeString(it.URL), html.EscapeString(it.URL))

	var meta []string
	if !it.SavedAt.IsZero() {
		meta = append(meta, "Saved "+it.SavedAt.Format("2 Jan 2006"))
	}
	if it.Publisher != "" {
		meta = append(meta, html.EscapeString(it.Publisher))
	}
	if len(it.Tags) > 0 {
		escaped := make([]string, len(it.Tags))
		for i, t := range it.Tags {
			escaped[i] = html.EscapeString(t)
		}
		meta = append(meta, "Tags: "+strings.Join(escaped, ", "))
	}
	if len(meta) > 0 {
		fmt.Fprintf(&b, "<p><small>%s</small></p>", strings.Join(meta, " &middot; "))
	}

	return b.String()
}

// imageType guesses an enclosure MIME type from the image URL's extension.
// It returns "" when the URL is empty or the type is not a known image, so we
// never advertise an enclosure we cannot describe honestly.
func imageType(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch strings.ToLower(path.Ext(u.Path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	default:
		return ""
	}
}

// RenderFeed produces the RSS 2.0 document for the given items. Items are
// expected oldest-first; the feed emits them newest-first.
func RenderFeed(cfg *Config, items []Item, now time.Time) ([]byte, error) {
	ch := channel{
		Title:       cfg.FeedTitle,
		Link:        cfg.KarakeepURL,
		Description: cfg.FeedDesc,
		Generator:   "karakeep-random-rss",
		Language:    "en",
		LastBuild:   now.UTC().Format(time.RFC1123Z),
		TTL:         int(cfg.Interval.Minutes()),
	}
	if cfg.PublicURL != "" {
		ch.AtomLink = &atomLink{Href: cfg.PublicURL, Rel: "self", Type: "application/rss+xml"}
	}

	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		ri := rssItem{
			Title:       cfg.ItemTitlePrefix + it.Title,
			Link:        it.URL,
			GUID:        guid{Value: it.GUID, IsPermaLink: "false"},
			PubDate:     it.PublishedAt.UTC().Format(time.RFC1123Z),
			Description: renderDescription(it),
			Categories:  it.Tags,
		}
		if mt := imageType(it.ImageURL); mt != "" {
			ri.Enclosure = &enclosure{URL: it.ImageURL, Type: mt}
		}
		ch.Items = append(ch.Items, ri)
	}

	doc := rss{
		Version:   "2.0",
		AtomNS:    "http://www.w3.org/2005/Atom",
		ContentNS: "http://purl.org/rss/1.0/modules/content/",
		Channel:   ch,
	}

	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
