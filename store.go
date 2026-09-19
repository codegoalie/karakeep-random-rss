package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Item is one published entry in the feed.
type Item struct {
	GUID        string    `json:"guid"`
	BookmarkID  string    `json:"bookmarkId"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Description string    `json:"description"`
	Note        string    `json:"note,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	ImageURL    string    `json:"imageUrl,omitempty"`
	Author      string    `json:"author,omitempty"`
	Publisher   string    `json:"publisher,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	SavedAt     time.Time `json:"savedAt"`
	PublishedAt time.Time `json:"publishedAt"`
}

// State is everything that must survive a restart.
type State struct {
	// Cycle counts how many times the library has been exhausted. It is part
	// of each GUID so a link resurfacing in a later cycle reads as a new item
	// in Miniflux instead of being deduplicated away.
	Cycle int `json:"cycle"`
	// SeenIDs are the bookmark IDs already published in the current cycle.
	SeenIDs []string `json:"seenIds"`
	// NextPublishAt keeps the schedule stable across restarts.
	NextPublishAt time.Time `json:"nextPublishAt"`
	Items         []Item    `json:"items"`
}

// Store persists State to a JSON file, atomically.
type Store struct {
	path string
	mu   sync.RWMutex
	st   State
}

func NewStore(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("reading state file: %w", err)
	}
	if len(b) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.st); err != nil {
		return nil, fmt.Errorf("parsing state file %s: %w", path, err)
	}
	return s, nil
}

// Snapshot returns a copy of the current state, safe to read without locking.
func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := s.st
	cp.SeenIDs = append([]string(nil), s.st.SeenIDs...)
	cp.Items = append([]Item(nil), s.st.Items...)
	return cp
}

// Update mutates the state under lock and writes it to disk.
func (s *Store) Update(fn func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.st)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if dir := filepath.Dir(s.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating state dir: %w", err)
		}
	}
	b, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replacing state: %w", err)
	}
	return nil
}

// randIndex returns a uniform random index in [0, n) using crypto/rand, so we
// never have to think about seeding.
func randIndex(n int) int {
	if n <= 1 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		// crypto/rand failing is fatal on Linux in practice; degrade to
		// something rather than panicking a long-running daemon.
		return int(time.Now().UnixNano()) % n
	}
	return int(v.Int64())
}

// Pick chooses a random bookmark that has not been published in the current
// cycle. When every bookmark has had a turn it starts a fresh cycle, avoiding
// an immediate repeat of the most recently published links.
//
// It returns the chosen bookmark and the cycle it belongs to.
func Pick(pool []Bookmark, st State) (Bookmark, int, bool) {
	if len(pool) == 0 {
		return Bookmark{}, st.Cycle, false
	}

	seen := make(map[string]bool, len(st.SeenIDs))
	for _, id := range st.SeenIDs {
		seen[id] = true
	}

	candidates := make([]Bookmark, 0, len(pool))
	for _, b := range pool {
		if !seen[b.ID] {
			candidates = append(candidates, b)
		}
	}
	cycle := st.Cycle

	if len(candidates) == 0 {
		// Library exhausted: start a new cycle. Hold back the tail of the
		// previous cycle so the feed does not repeat itself back to back.
		cycle++
		cooldown := recentIDs(st.Items, coolDownSize(len(pool)))
		for _, b := range pool {
			if !cooldown[b.ID] {
				candidates = append(candidates, b)
			}
		}
		if len(candidates) == 0 {
			candidates = append(candidates, pool...)
		}
	}

	return candidates[randIndex(len(candidates))], cycle, true
}

// coolDownSize keeps roughly a tenth of the library out of the running right
// after a cycle rolls over, capped so small libraries stay usable.
func coolDownSize(poolSize int) int {
	n := poolSize / 10
	if n > 25 {
		n = 25
	}
	if n >= poolSize {
		n = poolSize - 1
	}
	if n < 0 {
		n = 0
	}
	return n
}

func recentIDs(items []Item, n int) map[string]bool {
	out := map[string]bool{}
	if n <= 0 {
		return out
	}
	for i := len(items) - 1; i >= 0 && len(out) < n; i-- {
		out[items[i].BookmarkID] = true
	}
	return out
}
