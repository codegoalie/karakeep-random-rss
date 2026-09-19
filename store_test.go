package main

import (
	"path/filepath"
	"testing"
	"time"
)

func makePool(n int) []Bookmark {
	pool := make([]Bookmark, n)
	for i := range pool {
		pool[i] = Bookmark{
			ID:      string(rune('a' + i)),
			Title:   "Bookmark",
			Content: Content{Type: "link", URL: "https://example.com/"},
		}
	}
	return pool
}

// The whole point of the no-repeat mode: every bookmark gets a turn before any
// bookmark gets a second turn.
func TestPickExhaustsPoolBeforeRepeating(t *testing.T) {
	pool := makePool(12)
	st := State{}

	seenCounts := map[string]int{}
	for i := 0; i < len(pool); i++ {
		b, cycle, ok := Pick(pool, st)
		if !ok {
			t.Fatalf("pick %d: not ok", i)
		}
		if cycle != 0 {
			t.Fatalf("pick %d: cycle rolled early to %d", i, cycle)
		}
		seenCounts[b.ID]++
		st.SeenIDs = append(st.SeenIDs, b.ID)
		st.Items = append(st.Items, Item{BookmarkID: b.ID})
	}

	if len(seenCounts) != len(pool) {
		t.Fatalf("expected %d distinct bookmarks, got %d", len(pool), len(seenCounts))
	}
	for id, n := range seenCounts {
		if n != 1 {
			t.Fatalf("bookmark %s published %d times in one cycle", id, n)
		}
	}
}

func TestPickRollsToNewCycleWhenExhausted(t *testing.T) {
	pool := makePool(5)
	st := State{Cycle: 3}
	for _, b := range pool {
		st.SeenIDs = append(st.SeenIDs, b.ID)
		st.Items = append(st.Items, Item{BookmarkID: b.ID})
	}

	b, cycle, ok := Pick(pool, st)
	if !ok {
		t.Fatal("expected a pick after exhaustion")
	}
	if cycle != 4 {
		t.Fatalf("expected cycle 4, got %d", cycle)
	}
	if b.ID == "" {
		t.Fatal("expected a bookmark")
	}
	if MakeGUID(b.ID, cycle) == MakeGUID(b.ID, 3) {
		t.Fatal("GUID must change across cycles so the item reads as new")
	}
}

// A single-bookmark library must still work rather than deadlock.
func TestPickSingleBookmarkLibrary(t *testing.T) {
	pool := makePool(1)
	st := State{SeenIDs: []string{pool[0].ID}, Items: []Item{{BookmarkID: pool[0].ID}}}

	b, cycle, ok := Pick(pool, st)
	if !ok || b.ID != pool[0].ID || cycle != 1 {
		t.Fatalf("got %v cycle=%d ok=%v", b.ID, cycle, ok)
	}
}

func TestPickEmptyPool(t *testing.T) {
	if _, _, ok := Pick(nil, State{}); ok {
		t.Fatal("expected no pick from an empty pool")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")

	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	next := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err := s.Update(func(st *State) {
		st.Cycle = 2
		st.SeenIDs = []string{"x", "y"}
		st.NextPublishAt = next
		st.Items = []Item{{GUID: "g", BookmarkID: "x", Title: "T"}}
	}); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.Snapshot()
	if got.Cycle != 2 || len(got.SeenIDs) != 2 || len(got.Items) != 1 {
		t.Fatalf("state did not survive a restart: %+v", got)
	}
	if !got.NextPublishAt.Equal(next) {
		t.Fatalf("next publish time drifted: %s vs %s", got.NextPublishAt, next)
	}
}

// Snapshot must not hand out the live slices.
func TestSnapshotIsACopy(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(st *State) { st.SeenIDs = []string{"a"} }); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	snap.SeenIDs[0] = "mutated"
	if s.Snapshot().SeenIDs[0] != "a" {
		t.Fatal("Snapshot exposed internal state")
	}
}
