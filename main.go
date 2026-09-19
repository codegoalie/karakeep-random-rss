package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("karakeep-random-rss: ")

	cfg, err := LoadConfig(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		log.Fatalf("config: %v", err)
	}

	store, err := NewStore(cfg.StateFile)
	if err != nil {
		log.Fatalf("state: %v", err)
	}

	srv := &Server{
		cfg:    cfg,
		store:  store,
		client: NewClient(cfg.KarakeepURL, cfg.APIKey, cfg.HTTPTimeout),
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("%v", err)
	}
	log.Print("shut down cleanly")
}

// Server owns the schedule, the state and the HTTP listener.
type Server struct {
	cfg    *Config
	store  *Store
	client *Client

	mu       sync.Mutex // serialises publishes (scheduler vs. POST /publish)
	lastErr  error
	lastSync time.Time
}

func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/feed.xml", s.handleFeed)
	mux.HandleFunc("/rss", s.handleFeed)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/publish", s.handlePublish)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/feed.xml", http.StatusFound)
	})

	httpSrv := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("serving feed on %s/feed.xml (every %s)", s.cfg.Listen, s.cfg.Interval)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go s.schedule(ctx)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

// schedule publishes on the configured interval, carrying the next due time in
// the state file so restarts do not reset the clock.
func (s *Server) schedule(ctx context.Context) {
	for {
		st := s.store.Snapshot()
		wait := time.Duration(0)
		if !st.NextPublishAt.IsZero() {
			wait = time.Until(st.NextPublishAt)
		}

		if wait > 0 {
			log.Printf("next link in %s (at %s)", wait.Round(time.Second),
				st.NextPublishAt.Local().Format(time.RFC1123))
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		if err := s.publish(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("publish failed: %v", err)
			// Back off briefly, then retry without burning through the
			// schedule: NextPublishAt is only advanced on success.
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay(s.cfg.Interval)):
			}
		}
	}
}

// retryDelay waits a tenth of the interval, clamped to [1m, 15m].
func retryDelay(interval time.Duration) time.Duration {
	d := interval / 10
	if d < time.Minute {
		d = time.Minute
	}
	if d > 15*time.Minute {
		d = 15 * time.Minute
	}
	return d
}

// publish fetches the library, picks an unseen link and appends it to the feed.
func (s *Server) publish(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	fetchCtx, cancel := context.WithTimeout(ctx, s.cfg.HTTPTimeout+30*time.Second)
	defer cancel()

	pool, err := s.client.FetchLinks(fetchCtx, s.cfg.ListName, s.cfg.Tag, s.cfg.MaxPages)
	if err != nil {
		s.lastErr = err
		return err
	}
	s.lastSync = time.Now()
	s.lastErr = nil

	if len(pool) == 0 {
		return errors.New("no link bookmarks found in Karakeep with the current filters")
	}

	st := s.store.Snapshot()
	chosen, cycle, ok := Pick(pool, st)
	if !ok {
		return errors.New("nothing to pick")
	}

	now := time.Now().UTC()
	item := BuildItem(chosen, cycle, now)

	err = s.store.Update(func(st *State) {
		if cycle != st.Cycle {
			log.Printf("library exhausted after %d links; starting cycle %d",
				len(st.SeenIDs), cycle)
			st.Cycle = cycle
			st.SeenIDs = nil
		}
		st.SeenIDs = append(st.SeenIDs, chosen.ID)
		st.Items = append(st.Items, item)
		if len(st.Items) > s.cfg.FeedItems {
			st.Items = st.Items[len(st.Items)-s.cfg.FeedItems:]
		}
		st.NextPublishAt = now.Add(s.cfg.Interval)
	})
	if err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	log.Printf("published %q (%s) — %d of %d links used this cycle",
		item.Title, item.URL, len(st.SeenIDs)+1, len(pool))
	return nil
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	st := s.store.Snapshot()
	body, err := RenderFeed(s.cfg, st.Items, time.Now())
	if err != nil {
		http.Error(w, "failed to render feed", http.StatusInternalServerError)
		log.Printf("render: %v", err)
		return
	}

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	if len(st.Items) > 0 {
		last := st.Items[len(st.Items)-1]
		w.Header().Set("Last-Modified", last.PublishedAt.UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", `"`+etag(last.GUID, len(st.Items))+`"`)
		if match := r.Header.Get("If-None-Match"); match != "" &&
			strings.Contains(match, etag(last.GUID, len(st.Items))) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Write(body)
}

func etag(guid string, n int) string {
	return fmt.Sprintf("%x-%d", len(guid)+n, n)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	st := s.store.Snapshot()
	s.mu.Lock()
	lastErr, lastSync := s.lastErr, s.lastSync
	s.mu.Unlock()

	status := "ok"
	code := http.StatusOK
	if lastErr != nil {
		status = "degraded: " + lastErr.Error()
		code = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, "status: %s\nitems: %d\ncycle: %d\nseen this cycle: %d\nnext publish: %s\nlast sync: %s\n",
		status, len(st.Items), st.Cycle, len(st.SeenIDs),
		formatTime(st.NextPublishAt), formatTime(lastSync))
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format(time.RFC3339)
}

// handlePublish forces an out-of-band publish. Disabled unless ADMIN_TOKEN is set.
func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdminToken == "" {
		http.Error(w, "publishing on demand is disabled; set ADMIN_TOKEN to enable",
			http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+s.cfg.AdminToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := s.publish(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	st := s.store.Snapshot()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "published: %s\n", st.Items[len(st.Items)-1].Title)
}
