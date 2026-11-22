package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// logf logs a diagnostic for the person running the server; nothing logged reaches a client.
type logf func(format string, args ...any)

// Options configures the server.
type Options struct {
	Reports  reporting.BuildTimelineReport
	Sessions reporting.ListSessions
	Prompts  reporting.ReviewPrompts
	// Sync, when set, runs at startup and on request. Serve cancels and waits for it when its context ends.
	Sync Sync
	// ParseSince reads a period start as the command line states it (7d, 12h, 2006-01-02).
	ParseSince func(s string, now time.Time) (time.Time, error)
	// Assets is the built front: index.html and the files it references, at the root.
	Assets fs.FS
	// Since and Harness are the filters the page opens on; Since is a period as ParseSince reads it, "" for all.
	Since   string
	Harness kernel.Harness
	// Project is the project of the folder the server runs in, the zero Project when none was detected. Restricted says
	// whether the page opens on it rather than on every project. The page can name only these two scopes: the folder is
	// the server's to decide, never a request's.
	Project    kernel.Project
	Restricted bool
	// Detect, when set, finds the project of the folder the server runs in again each time the indexing ends, before the
	// page is told it did: a folder that no session had worked in may have been given sessions by it. A project found where
	// there was none becomes the one the page opens on, and a failure is logged and leaves the scope as it is. It is left
	// nil when the scope was named (--all, --project), which detection must not change.
	Detect func() (kernel.Project, error)
	// AllowRemote lifts the Host header allowlist: the server is meant to be reached under other names than loopback.
	AllowRemote bool
	// ShutdownGrace is the time the requests in flight get to end when the server stops; 5 s when zero.
	ShutdownGrace time.Duration
	Log           *log.Logger // diagnostics; nil discards them
	Now           func() time.Time
}

// defaultShutdownGrace is the time the requests in flight get to end when the server stops.
const defaultShutdownGrace = 5 * time.Second

// Server is the HTTP handler of the web interface.
type Server struct {
	opts         Options
	localPrompts bool
	indexCtx     context.Context
	port         string // the port listened on, which Host headers must carry
	sync         *syncer
	log          logf
	now          func() time.Time
	parseSince   func(string, time.Time) (time.Time, error)
	mux          *http.ServeMux

	mu         sync.Mutex
	stopped    bool           // no request starts any more
	inflight   sync.WaitGroup // the requests being answered
	project    kernel.Project // the project of Options, as detection last found it
	restricted bool
}

// New builds the handler for a server listening on addr. It does not start the indexing: Serve does.
func New(o Options, addr net.Addr) *Server {
	s := &Server{opts: o, now: o.Now, parseSince: o.ParseSince, log: func(string, ...any) {}, project: o.Project, restricted: o.Restricted}
	if s.now == nil {
		s.now = time.Now
	}
	if o.Log != nil {
		s.log = o.Log.Printf
	}
	host, port, _ := net.SplitHostPort(addr.String())
	s.port = port
	s.localPrompts = !o.AllowRemote && isLoopbackHost(host)
	s.sync = newSyncer(s.now, s.log)
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/api/meta", s.meta)
	s.mux.HandleFunc("/api/sync", s.syncProgress)
	s.mux.HandleFunc("/api/report", s.report)
	s.mux.HandleFunc("/api/sessions", s.sessions)
	s.mux.HandleFunc("/api/sessions/{id}", s.session)
	s.mux.HandleFunc("/api/prompts", s.prompts)
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		problem{Type: problemNotFound, Title: "No such route", Status: http.StatusNotFound}.write(w)
	})
	s.mux.HandleFunc("/", s.static)
	return s
}

// coverage is the project the server runs in and whether the page opens on it, as detection last found them.
func (s *Server) coverage() (kernel.Project, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.project, s.restricted
}

// indexing is the indexing the server runs: Options.Sync, followed by a new detection of the project when the scope was
// not named. The detection runs inside it, so that the page sees the indexing end only once the scope is decided.
func (s *Server) indexing() Sync {
	if s.opts.Sync == nil || s.opts.Detect == nil {
		return s.opts.Sync
	}
	return func(ctx context.Context, progress func(int)) (string, error) {
		note, err := s.opts.Sync(ctx, progress)
		if ctx.Err() != nil { // the server is stopping
			return note, err
		}
		found, derr := s.opts.Detect()
		if derr != nil {
			s.log("detecting the project: %v", derr)
			return note, err
		}
		s.mu.Lock()
		s.project, s.restricted = reporting.Redetect(s.project, s.restricted, found)
		s.mu.Unlock()
		return note, err
	}
}

// contentSecurityPolicy lets the page load its own scripts, styles and data and nothing else: no inline script,
// no other origin, no framing.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
	"connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// ServeHTTP checks the request is one this server answers, sets the security headers and routes it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	// No Access-Control-* header is ever set: browsers keep other origins from reading any response.
	if strings.HasPrefix(r.URL.Path, "/api/") {
		h.Set("Cache-Control", "no-store")
	}

	if !s.enter() {
		problem{Type: problemStopping, Title: "Server stopping", Status: http.StatusServiceUnavailable}.write(w)
		return
	}
	defer s.inflight.Done()

	if !s.opts.AllowRemote && !hostAllowed(r.Host, s.port) {
		problem{Type: problemHostNotAllowed, Title: "Host not allowed", Status: http.StatusForbidden,
			Detail: "this server answers requests addressed to localhost, 127.0.0.1 or [::1] on its own port"}.write(w)
		return
	}
	syncRoute := r.URL.Path == "/api/sync"
	reindex := syncRoute && r.Method == http.MethodPost
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !reindex {
		allow := "GET, HEAD"
		if syncRoute {
			allow += ", POST"
		}
		h.Set("Allow", allow)
		problem{Type: problemMethodNotAllowed, Title: "Method not allowed", Status: http.StatusMethodNotAllowed}.write(w)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		site := r.Header.Get("Sec-Fetch-Site")
		if !fromOwnPage(site) || (reindex && site != "same-origin") {
			problem{Type: problemForeignOrigin, Title: "Request not made by this page refused", Status: http.StatusForbidden}.write(w)
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// enter registers a request about to be answered, unless the server has stopped taking them.
func (s *Server) enter() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	s.inflight.Add(1)
	return true
}

// drain refuses the requests that have not started and waits for those in flight: once it returns, nothing reads the
// index any more.
func (s *Server) drain() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	s.inflight.Wait()
}

// fromOwnPage reports whether a Sec-Fetch-Site value is one the API answers: the page this server served (same-origin),
// the user typing the address (none), or a client that sends no such header, like curl. "same-site" is refused: another
// port of this machine is the same site, and no other local application may read the prompts.
func fromOwnPage(secFetchSite string) bool {
	return secFetchSite == "" || secFetchSite == "same-origin" || secFetchSite == "none"
}

// hostAllowed reports whether a Host header names a loopback host on the port the server listens on. A request that
// reaches the server under another name, as in a DNS rebinding attack, is refused.
func hostAllowed(hostHeader, port string) bool {
	host, p, err := net.SplitHostPort(hostHeader)
	if err != nil { // no port: the scheme's default, 80
		host, p = strings.Trim(hostHeader, "[]"), "80"
	}
	if p != port {
		return false
	}
	return isLoopbackHost(host)
}

// static serves the built front. There is one page and no client-side routing, so any other path is a 404.
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	info, err := fs.Stat(s.opts.Assets, name)
	if err != nil || info.IsDir() || !fs.ValidPath(name) {
		problem{Type: problemNotFound, Title: "Not found", Status: http.StatusNotFound}.write(w)
		return
	}
	switch {
	case strings.HasPrefix(name, "assets/"): // the build names them after their content
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, s.opts.Assets, name)
}

// Serve answers on ln until ctx is done, indexing in the background when Options.Sync is set. It returns once the
// requests in flight are over and the indexing has stopped, so the caller may close the index. Requests get
// the grace to finish; the ones still running then are cancelled and waited for, and Serve returns the error of
// the shutdown that did not complete in time.
func Serve(ctx context.Context, ln net.Listener, o Options) error {
	grace := cmp.Or(o.ShutdownGrace, defaultShutdownGrace)
	// The requests outlive the stop signal, up to the grace; they end early only when the grace is over.
	requests, endRequests := context.WithCancel(context.WithoutCancel(ctx))
	defer endRequests()
	ctx, cancel := context.WithCancel(ctx) // also ends the indexing when the server stops on its own, on an error
	s := New(o, ln.Addr())
	s.indexCtx = ctx
	var errLog *log.Logger = o.Log
	if errLog == nil {
		errLog = log.New(io.Discard, "", 0)
	}
	srv := &http.Server{
		Handler:           s,
		BaseContext:       func(net.Listener) context.Context { return requests },
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          errLog,
	}
	s.sync.start(ctx, s.indexing())
	defer func() {
		cancel()
		s.sync.jobs.Wait()
	}()
	var shutdownErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		graceEnds, endGrace := context.WithTimeout(context.Background(), grace)
		defer endGrace()
		if err := srv.Shutdown(graceEnds); err != nil {
			shutdownErr = fmt.Errorf("stopping the server: %w", err)
			endRequests() // the queries still running stop...
			srv.Close()
		}
		s.drain() // ...and are waited for, so that nothing reads the index once Serve has returned
	}()
	err := srv.Serve(ln)
	cancel()
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		return shutdownErr
	}
	return err
}
