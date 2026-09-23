package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds every knob the service exposes. Every flag has a matching
// environment variable so the same binary works under systemd, Docker and a
// bare shell. Flags win over environment variables.
type Config struct {
	KarakeepURL     string        // base URL of the Karakeep instance
	APIKey          string        // Karakeep API key (Settings > API Keys)
	Listen          string        // host:port to serve the feed on
	Interval        time.Duration // how often a new random link is published
	StateFile       string        // where the seen-set and published items live
	FeedItems       int           // how many items to keep in the feed
	FeedTitle       string
	FeedDesc        string
	ItemTitlePrefix string // prepended to each item's title at render time
	PublicURL       string // public URL of the feed, used for the atom self link
	HTTPTimeout     time.Duration
	AdminToken      string // optional; enables POST /publish when set
	Tag             string // optional tag filter (client side)
	ListName        string // optional list filter (client side)
	MaxPages        int    // safety valve on bookmark pagination
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDurationOr(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s=%q is not a duration, using %s\n", key, v, def)
		return def
	}
	return d
}

func envIntOr(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s=%q is not a number, using %d\n", key, v, def)
		return def
	}
	return n
}

// LoadConfig parses flags and environment variables and validates the result.
func LoadConfig(args []string) (*Config, error) {
	c := &Config{}
	fs := flag.NewFlagSet("karakeep-random-rss", flag.ContinueOnError)

	fs.StringVar(&c.KarakeepURL, "karakeep-url", envOr("KARAKEEP_URL", ""),
		"base URL of your Karakeep instance (env KARAKEEP_URL)")
	fs.StringVar(&c.APIKey, "api-key", envOr("KARAKEEP_API_KEY", ""),
		"Karakeep API key (env KARAKEEP_API_KEY, or KARAKEEP_API_KEY_FILE)")
	fs.StringVar(&c.Listen, "listen", envOr("LISTEN", ":8080"),
		"address to listen on (env LISTEN)")
	fs.DurationVar(&c.Interval, "interval", envDurationOr("INTERVAL", 24*time.Hour),
		"how often to publish a new random link, e.g. 24h, 8h, 168h (env INTERVAL)")
	fs.StringVar(&c.StateFile, "state-file", envOr("STATE_FILE", "state.json"),
		"path to the JSON state file (env STATE_FILE)")
	fs.IntVar(&c.FeedItems, "feed-items", envIntOr("FEED_ITEMS", 50),
		"number of items to keep in the feed (env FEED_ITEMS)")
	fs.StringVar(&c.FeedTitle, "feed-title", envOr("FEED_TITLE", "Karakeep: a random link"),
		"feed title (env FEED_TITLE)")
	fs.StringVar(&c.ItemTitlePrefix, "item-title-prefix", envOr("ITEM_TITLE_PREFIX", "🔖 From the stacks: "),
		"prefix added to each item's title so readers recognize it as resurfaced, not original (env ITEM_TITLE_PREFIX; pass -item-title-prefix=\"\" to disable)")
	fs.StringVar(&c.FeedDesc, "feed-description", envOr("FEED_DESCRIPTION",
		"One random bookmark from my Karakeep library, resurfaced on a schedule."),
		"feed description (env FEED_DESCRIPTION)")
	fs.StringVar(&c.PublicURL, "public-url", envOr("PUBLIC_URL", ""),
		"public URL of the feed, used for the atom:link self reference (env PUBLIC_URL)")
	fs.DurationVar(&c.HTTPTimeout, "http-timeout", envDurationOr("HTTP_TIMEOUT", 30*time.Second),
		"timeout for requests to Karakeep (env HTTP_TIMEOUT)")
	fs.StringVar(&c.AdminToken, "admin-token", envOr("ADMIN_TOKEN", ""),
		"if set, enables POST /publish with this bearer token (env ADMIN_TOKEN)")
	fs.StringVar(&c.Tag, "tag", envOr("TAG", ""),
		"only consider bookmarks carrying this tag (env TAG)")
	fs.StringVar(&c.ListName, "list", envOr("LIST", ""),
		"only consider bookmarks in this Karakeep list (env LIST)")
	fs.IntVar(&c.MaxPages, "max-pages", envIntOr("MAX_PAGES", 200),
		"maximum bookmark pages to fetch per refresh (env MAX_PAGES)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// Docker/systemd secret support: read the key out of a file if asked.
	if keyFile := os.Getenv("KARAKEEP_API_KEY_FILE"); keyFile != "" && c.APIKey == "" {
		b, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("reading KARAKEEP_API_KEY_FILE: %w", err)
		}
		c.APIKey = strings.TrimSpace(string(b))
	}

	if c.KarakeepURL == "" {
		return nil, fmt.Errorf("karakeep URL is required (-karakeep-url or KARAKEEP_URL)")
	}
	if c.APIKey == "" {
		return nil, fmt.Errorf("API key is required (-api-key, KARAKEEP_API_KEY or KARAKEEP_API_KEY_FILE)")
	}

	u, err := url.Parse(c.KarakeepURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("karakeep URL %q must be absolute, e.g. https://karakeep.example.com", c.KarakeepURL)
	}
	c.KarakeepURL = strings.TrimRight(c.KarakeepURL, "/")

	if c.Interval < time.Minute {
		return nil, fmt.Errorf("interval %s is too short; use at least 1m", c.Interval)
	}
	if c.FeedItems < 1 {
		c.FeedItems = 1
	}
	if c.MaxPages < 1 {
		c.MaxPages = 1
	}

	return c, nil
}
