package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

const listenPort = "7373"

var fixedNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// request is one indexed request of a ledger.
type request struct {
	at      time.Time
	harness kernel.Harness
	cost    domain.USD
}

// index is a reporting index that records the filter of the questions it is asked. Given a ledger, it totals the requests
// the filter keeps; without one, it answers fixed figures.
type index struct {
	filters []domain.Filter
	err     error
	ledger  []request
}

func (x *index) Snapshot(fn func(reporting.UsageReader) error) error {
	if x.err != nil {
		return x.err
	}
	return fn(x)
}

func (x *index) Total(f domain.Filter) (domain.Usage, error) {
	x.filters = append(x.filters, f)
	if x.ledger != nil {
		var u domain.Usage
		for _, r := range x.ledger {
			if (f.Since.IsZero() || !r.at.Before(f.Since)) && (f.Harness.IsZero() || f.Harness == r.harness) {
				u.Requests++
				u.Cost += r.cost
			}
		}
		return u, nil
	}
	return domain.Usage{Requests: 3, Cost: 1.5, CacheRead: 30, Input: 10}, nil
}

func (x *index) UsageBy(_ domain.Filter, d domain.Dimension) ([]domain.UsageGroup, error) {
	switch d {
	case domain.ByHarness:
		return []domain.UsageGroup{{Group: "claude", Usage: domain.Usage{Requests: 3, Cost: 1.5}}}, nil
	case domain.ByDay:
		return []domain.UsageGroup{{Group: "2026-11-19", Usage: domain.Usage{Requests: 2, Cost: 1}}, {Group: "2026-11-20", Usage: domain.Usage{Requests: 1, Cost: 0.5}}}, nil
	}
	return nil, nil // an empty grouping
}

func (x *index) Tools(domain.Filter) ([]domain.ToolUsage, error) {
	return []domain.ToolUsage{{Name: "Bash", Calls: 4}}, nil
}

// sessionIndex lists as many sessions as it is told.
type sessionIndex struct {
	n   int
	err error
}

func (x sessionIndex) Sessions(domain.Filter) ([]domain.SessionSummary, error) {
	var out []domain.SessionSummary
	for i := 0; i < x.n; i++ {
		id, _ := domain.NewSessionID(fmt.Sprintf("s%d", i))
		out = append(out, domain.SessionSummary{ID: id, Harness: kernel.Claude, Title: "title", Usage: domain.Usage{Requests: 1, Cost: 1},
			Models: []domain.UsageGroup{{Group: "m", Usage: domain.Usage{Requests: 1, Cost: 1}}}})
	}
	return out, x.err
}

func (x sessionIndex) Session(domain.SessionID) (domain.SessionDetail, error) {
	return domain.SessionDetail{}, domain.ErrSessionNotFound
}

// parseSince accepts what the command line does, for the periods the tests use.
func parseSince(s string, now time.Time) (time.Time, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		var d int
		if _, err := fmt.Sscanf(n, "%d", &d); err == nil {
			return now.AddDate(0, 0, -d), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}
	return time.Parse("2006-01-02", s)
}

type rig struct {
	idx      *index
	handler  *Server
	logLines []string
}

func newRig(t *testing.T, mutate ...func(*Options)) *rig {
	t.Helper()
	r := &rig{idx: &index{}}
	o := Options{
		Reports:    reporting.BuildTimelineReport{Queries: r.idx},
		Sessions:   reporting.ListSessions{Queries: sessionIndex{n: 3}},
		ParseSince: parseSince,
		Assets: fstest.MapFS{
			"index.html":           {Data: []byte("<!doctype html><title>agent-stats</title>")},
			"assets/app-abc123.js": {Data: []byte("console.log(1)")},
		},
		Since: "7d",
		Now:   func() time.Time { return fixedNow },
	}
	for _, m := range mutate {
		m(&o)
	}
	r.handler = New(o, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7373})
	r.handler.log = func(f string, a ...any) { r.logLines = append(r.logLines, fmt.Sprintf(f, a...)) }
	r.handler.sync.log = r.handler.log
	return r
}

func (r *rig) get(target string, headers ...string) *httptest.ResponseRecorder {
	return r.do(http.MethodGet, target, headers...)
}

func (r *rig) do(method, target string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://localhost:"+listenPort+target, nil)
	req.Host = "localhost:" + listenPort
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i] == "Host" {
			req.Host = headers[i+1]
			continue
		}
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body is not the expected JSON: %v\n%s", err, rec.Body.String())
	}
	return v
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, typ string) problem {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content type = %q, want a problem document", ct)
	}
	p := decode[problem](t, rec)
	if p.Type != typ || p.Status != status {
		t.Errorf("problem = %+v, want type %s status %d", p, typ, status)
	}
	return p
}

func TestReportIsServedWithTheDaysAndTheCommandLineFields(t *testing.T) {
	// Given an index with usage on two days
	r := newRig(t)

	// When the page asks for the report
	rec := r.get("/api/report")

	// Then it gets the figures of the command line's --json report, plus the days, oldest first
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	got := decode[reportJSON](t, rec)
	if got.Total.Requests != 3 || got.Total.Cost != 1.5 || got.Total.CacheRate != 0.75 {
		t.Errorf("total = %+v", got.Total)
	}
	if len(got.Daily) != 2 || got.Daily[0].Key != "2026-11-19" || got.Daily[1].Cost != 0.5 {
		t.Errorf("daily = %+v", got.Daily)
	}
	if len(got.ByHarness) != 1 || got.ByHarness[0].Key != "claude" || len(got.Tools) != 1 || got.Tools[0].Name != "Bash" {
		t.Errorf("groupings = %+v", got)
	}
}

func TestReportListsAreEmptyNotNull(t *testing.T) {
	// Given an index with no model, folder or agent usage
	r := newRig(t)

	// When the report is read as raw JSON
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(r.get("/api/report").Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}

	// Then those lists are [] so that the page never has to guard against null
	for _, key := range []string{"byModel", "byFolder", "byAgentType"} {
		if string(raw[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, raw[key])
		}
	}
}

func TestReportCoversOnlyThePeriodAndTheHarnessAskedFor(t *testing.T) {
	// Given an index with a claude request and a codex request from yesterday, and an older claude request
	r := newRig(t)
	r.idx.ledger = []request{
		{fixedNow.AddDate(0, 0, -1), kernel.Claude, 1},
		{fixedNow.AddDate(0, 0, -1), kernel.Codex, 2},
		{fixedNow.AddDate(0, 0, -10), kernel.Claude, 4},
	}

	// When the report is asked for the last 7 days of claude, for the last 7 days of every harness, and for all time
	claudeWeek := decode[reportJSON](t, r.get("/api/report?since=7d&harness=claude")).Total
	anyWeek := decode[reportJSON](t, r.get("/api/report?since=7d")).Total
	allTime := decode[reportJSON](t, r.get("/api/report")).Total

	// Then each answer counts exactly what that period and harness hold
	if claudeWeek.Requests != 1 || claudeWeek.Cost != 1 {
		t.Errorf("claude, 7 days = %+v, want 1 request costing 1", claudeWeek)
	}
	if anyWeek.Requests != 2 || anyWeek.Cost != 3 {
		t.Errorf("every harness, 7 days = %+v, want 2 requests costing 3", anyWeek)
	}
	if allTime.Requests != 3 || allTime.Cost != 7 {
		t.Errorf("all time = %+v, want 3 requests costing 7", allTime)
	}
}

func TestInvalidParametersAreRefusedWithTheParameterNamed(t *testing.T) {
	r := newRig(t)
	for _, c := range []struct {
		name, target, param string
		status              int
	}{
		{"unknown period", "/api/report?since=banana", "since", 400},
		{"period in the future", "/api/report?since=-3d", "since", 400},
		{"period before any agent log", "/api/report?since=1999-01-01", "since", 400},
		{"period too long", "/api/report?since=" + strings.Repeat("1", 33), "since", 400},
		{"unknown harness", "/api/report?harness=vim", "harness", 400},
		{"unknown parameter", "/api/report?folder=%2Fhome", "folder", 400},
		{"repeated parameter", "/api/report?harness=claude&harness=codex", "harness", 400},
		{"limit on the report", "/api/report?limit=5", "limit", 400},
		{"limit not a number", "/api/sessions?limit=many", "limit", 400},
		{"limit zero", "/api/sessions?limit=0", "limit", 400},
		{"limit over the bound", "/api/sessions?limit=1001", "limit", 400},
		{"malformed escape in the period", "/api/report?since=%ZZ", "", 400},
		{"malformed escape beside a valid parameter", "/api/report?harness=claude&since=%ZZ", "", 400},
		{"malformed escape on the sessions", "/api/sessions?limit=%ZZ", "", 400},
		{"semicolon as a separator", "/api/report?since=7d;harness=claude", "", 400},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Given a request with a parameter outside what the API accepts
			// When it is sent
			rec := r.get(c.target)

			// Then it is refused as an invalid parameter, naming the one at fault, and no figure is read
			p := wantProblem(t, rec, c.status, problemInvalidParameter)
			if p.Parameter != c.param {
				t.Errorf("parameter = %q, want %q", p.Parameter, c.param)
			}
		})
	}
	if len(r.idx.filters) != 0 {
		t.Errorf("the index was read for a refused request: %v", r.idx.filters)
	}
}

func TestAQueryStringOverTheBoundIsRefused(t *testing.T) {
	// Given a query string longer than any legitimate one
	r := newRig(t)

	// When it is sent
	rec := r.get("/api/report?since=7d&" + strings.Repeat("a=1&", 100))

	// Then the server answers 414 without reading the index
	wantProblem(t, rec, http.StatusRequestURITooLong, problemInvalidParameter)
}

func TestSessionsAreListedMostRecentFirstWithinTheLimit(t *testing.T) {
	// Given 3 sessions
	r := newRig(t)

	// When the first 2 are asked for
	got := decode[sessionsJSON](t, r.get("/api/sessions?limit=2"))

	// Then two are listed, the count and the total still cover all three, and no prompt text is exposed
	if len(got.Sessions) != 2 || got.Count != 3 || got.Total.Requests != 3 || got.Total.Cost != 3 {
		t.Errorf("sessions = %+v", got)
	}
	if s := got.Sessions[0]; s.ID != "s0" || s.Harness != "claude" || len(s.Models) != 1 || s.Models[0].Share != 1 {
		t.Errorf("first session = %+v", s)
	}
}

func TestSessionsHaveALimitByDefault(t *testing.T) {
	// Given far more sessions than a page needs
	r := newRig(t, func(o *Options) { o.Sessions = reporting.ListSessions{Queries: sessionIndex{n: defaultLimit + 50}} })

	// When no limit is given
	got := decode[sessionsJSON](t, r.get("/api/sessions"))

	// Then the default limit applies
	if len(got.Sessions) != defaultLimit || got.Count != defaultLimit+50 {
		t.Errorf("listed %d of %d", len(got.Sessions), got.Count)
	}
}

func TestAnIndexFailureIsAnInternalErrorThatLeaksNothing(t *testing.T) {
	// Given an index that fails with an error naming a path of the user's machine
	secret := "open /Users/someone/.cache/agent-stats/index.db: disk I/O error"
	r := newRig(t, func(o *Options) { o.Sessions = reporting.ListSessions{Queries: sessionIndex{err: errors.New(secret)}} })
	r.idx.err = errors.New(secret)

	for _, target := range []string{"/api/report", "/api/sessions"} {
		// When the page asks for figures
		rec := r.get(target)

		// Then it gets a bare 500, and the cause is in the server log only
		wantProblem(t, rec, http.StatusInternalServerError, problemInternal)
		if strings.Contains(rec.Body.String(), "someone") {
			t.Errorf("%s leaks the cause: %s", target, rec.Body.String())
		}
	}
	if len(r.logLines) != 2 || !strings.Contains(r.logLines[0], secret) {
		t.Errorf("log = %v", r.logLines)
	}
}

func TestMetaGivesTheFiltersTheServerOpensOn(t *testing.T) {
	// Given a server started for the last 30 days of pi
	r := newRig(t, func(o *Options) { o.Since, o.Harness = "30d", kernel.Pi })

	// When the page asks what it can filter on
	got := decode[metaJSON](t, r.get("/api/meta"))

	// Then it learns every harness and the defaults
	if strings.Join(got.Harnesses, ",") != "claude,codex,omp,pi" || got.Defaults.Since != "30d" || got.Defaults.Harness != "pi" {
		t.Errorf("meta = %+v", got)
	}
}

func TestHostHeaderMustBeALoopbackNameOnTheListeningPort(t *testing.T) {
	r := newRig(t)
	for _, c := range []struct {
		host    string
		allowed bool
	}{
		{"localhost:7373", true},
		{"LOCALHOST:7373", true},
		{"127.0.0.1:7373", true},
		{"[::1]:7373", true},
		{"evil.example:7373", false},
		{"localhost.evil.example:7373", false},
		{"127.0.0.1.evil.example:7373", false},
		{"localhost:80", false},
		{"localhost", false}, // the default port is not the listening one
		{"192.168.1.20:7373", false},
		{"", false},
	} {
		t.Run(c.host, func(t *testing.T) {
			// Given a request addressed to this Host, as a DNS rebinding page would
			// When it asks for the report
			rec := r.get("/api/report", "Host", c.host)

			// Then only the loopback names on the listening port are answered
			if c.allowed && rec.Code != http.StatusOK {
				t.Errorf("refused: %d", rec.Code)
			}
			if !c.allowed {
				wantProblem(t, rec, http.StatusForbidden, problemHostNotAllowed)
			}
		})
	}
}

func TestHostHeaderIsCheckedForThePageToo(t *testing.T) {
	// Given a rebinding request for the page itself
	r := newRig(t)

	// When it is served
	rec := r.get("/", "Host", "evil.example:7373")

	// Then it is refused as well
	wantProblem(t, rec, http.StatusForbidden, problemHostNotAllowed)
}

func TestRemoteAccessLiftsTheHostAllowlist(t *testing.T) {
	// Given a server started with --allow-remote, reached by its machine's name
	r := newRig(t, func(o *Options) { o.AllowRemote = true })

	// When a request carries that name
	rec := r.get("/api/meta", "Host", "workstation.lan:7373")

	// Then it is served
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestEveryResponseCarriesTheSecurityHeadersAndNoCORS(t *testing.T) {
	r := newRig(t)
	for _, target := range []string{"/", "/assets/app-abc123.js", "/api/report", "/api/nothing", "/missing", "/api/report?x=1"} {
		t.Run(target, func(t *testing.T) {
			// Given any route, success or error
			// When it is requested, even by a page of another origin
			rec := r.get(target, "Origin", "https://evil.example")

			// Then the page is locked down and no CORS header lets another origin read it
			h := rec.Header()
			csp := h.Get("Content-Security-Policy")
			for _, want := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'", "base-uri 'none'", "form-action 'none'"} {
				if !strings.Contains(csp, want) {
					t.Errorf("CSP %q lacks %q", csp, want)
				}
			}
			if strings.Contains(csp, "unsafe-") {
				t.Errorf("CSP allows unsafe sources: %q", csp)
			}
			for header, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY"} {
				if got := h.Get(header); got != want {
					t.Errorf("%s = %q, want %q", header, got, want)
				}
			}
			for name := range h {
				if strings.HasPrefix(strings.ToLower(name), "access-control-") {
					t.Errorf("CORS header %s set", name)
				}
			}
		})
	}
}

func TestOnlyReadsAreAnswered(t *testing.T) {
	r := newRig(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			// Given a request that is not a read, including a CORS preflight
			// When it is sent to the API or to the page
			for _, target := range []string{"/api/report", "/"} {
				rec := r.do(method, target)

				// Then it is refused, saying what is allowed, and no Access-Control-Allow header answers a preflight
				wantProblem(t, rec, http.StatusMethodNotAllowed, problemMethodNotAllowed)
				if rec.Header().Get("Allow") != "GET, HEAD" {
					t.Errorf("Allow = %q", rec.Header().Get("Allow"))
				}
			}
		})
	}
	if len(r.idx.filters) != 0 {
		t.Error("the index was read for a refused request")
	}
}

func TestHeadIsAnswered(t *testing.T) {
	r := newRig(t)
	if rec := r.do(http.MethodHead, "/api/meta"); rec.Code != http.StatusOK {
		t.Errorf("HEAD = %d", rec.Code)
	}
}

// refusedWithoutData checks the API answered a refusal and that no figure was read to answer it.
func refusedWithoutData(t *testing.T, r *rig, rec *httptest.ResponseRecorder) {
	t.Helper()
	wantProblem(t, rec, http.StatusForbidden, problemCrossSite)
	if len(r.idx.filters) != 0 || strings.Contains(rec.Body.String(), "total") || strings.Contains(rec.Body.String(), "Bash") {
		t.Errorf("a refused request got data: %s (index asked %d times)", rec.Body.String(), len(r.idx.filters))
	}
}

func TestACrossSiteRequestForTheAPIIsRefused(t *testing.T) {
	// Given a browser that says the request comes from another site
	r := newRig(t)

	// When it asks for the report
	rec := r.get("/api/report", "Sec-Fetch-Site", "cross-site")

	// Then the API refuses and gives no figure
	refusedWithoutData(t, r, rec)
}

func TestASameSiteRequestForTheAPIIsRefused(t *testing.T) {
	// Given a browser that says the request comes from another port of this machine, which is still the same site
	r := newRig(t)

	// When it asks for the report
	rec := r.get("/api/report", "Sec-Fetch-Site", "same-site")

	// Then the API refuses and gives no figure: another local application must not read the prompts
	refusedWithoutData(t, r, rec)
}

func TestARequestOfAnUnknownKindForTheAPIIsRefused(t *testing.T) {
	// Given a request whose Sec-Fetch-Site says something no browser sends today
	r := newRig(t)

	// When it asks for the report
	rec := r.get("/api/report", "Sec-Fetch-Site", "somewhere-else")

	// Then the API refuses: only the page itself, or the user typing the address, may read it
	refusedWithoutData(t, r, rec)
}

func TestThePageCallsTheAPIFromItsOwnOrigin(t *testing.T) {
	// Given a browser that says the request comes from the page this server served
	r := newRig(t)

	// When it asks for the report
	rec := r.get("/api/report", "Sec-Fetch-Site", "same-origin")

	// Then it gets the figures
	if rec.Code != http.StatusOK || decode[reportJSON](t, rec).Total.Requests != 3 {
		t.Errorf("same-origin API = %d %s", rec.Code, rec.Body.String())
	}
}

func TestTheUserTypingTheAPIAddressGetsTheFigures(t *testing.T) {
	// Given a browser that says the request comes from the address bar
	r := newRig(t)

	// When it asks for the report
	rec := r.get("/api/report", "Sec-Fetch-Site", "none")

	// Then it gets the figures
	if rec.Code != http.StatusOK || decode[reportJSON](t, rec).Total.Requests != 3 {
		t.Errorf("address bar API = %d %s", rec.Code, rec.Body.String())
	}
}

func TestACrossSiteNavigationStillGetsThePage(t *testing.T) {
	// Given a browser that says the request comes from another site
	r := newRig(t)

	// When it navigates to the page
	rec := r.get("/", "Sec-Fetch-Site", "cross-site")

	// Then the page, which holds no figure, is served
	if rec.Code != http.StatusOK {
		t.Errorf("page = %d", rec.Code)
	}
}

func TestTheAPIIsNeverCached(t *testing.T) {
	r := newRig(t)
	for _, target := range []string{"/api/report", "/api/sessions", "/api/meta", "/api/sync", "/api/nothing"} {
		if got := r.get(target).Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q", target, got)
		}
	}
}

func TestAnUnknownAPIRouteIsAJSONNotFound(t *testing.T) {
	r := newRig(t)
	wantProblem(t, r.get("/api/nothing"), http.StatusNotFound, problemNotFound)
}

func TestThePageIsServedAsHTML(t *testing.T) {
	// Given a built front
	r := newRig(t)

	// When the root is requested
	page := r.get("/")

	// Then it is the HTML page
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "agent-stats") || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") {
		t.Errorf("page = %d %q %q", page.Code, page.Header().Get("Content-Type"), page.Body.String())
	}
}

func TestThePageMustBeRevalidated(t *testing.T) {
	// Given a built front
	r := newRig(t)

	// When the root is requested
	page := r.get("/")

	// Then the browser revalidates it, so a new build is picked up
	if got := page.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("page Cache-Control = %q", got)
	}
}

func TestAHashedAssetIsCachedForGood(t *testing.T) {
	// Given a built front whose assets are named after their content
	r := newRig(t)

	// When an asset is requested
	asset := r.get("/assets/app-abc123.js")

	// Then it is served with a cache lifetime that never needs revalidation
	if asset.Code != http.StatusOK || !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset = %d %q", asset.Code, asset.Header().Get("Cache-Control"))
	}
}

func TestAScriptAssetIsServedAsJavaScript(t *testing.T) {
	// Given a built front
	r := newRig(t)

	// When a script is requested
	asset := r.get("/assets/app-abc123.js")

	// Then the browser is told it is JavaScript
	if ct := asset.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("asset content type = %q", ct)
	}
}

func TestOnlyFilesOfTheFrontAreServed(t *testing.T) {
	r := newRig(t)
	for _, target := range []string{"/missing", "/assets/", "/assets", "/../go.mod", "/%2e%2e/go.mod", "/assets/%2e%2e/%2e%2e/main.go", "/index.html/x"} {
		t.Run(target, func(t *testing.T) {
			// Given a path that names no file of the front, or tries to leave it
			// When it is requested
			rec := r.get(target)

			// Then there is no listing and no file: a JSON 404 (or the redirect net/http makes of a path with dots)
			if rec.Code == http.StatusOK {
				t.Fatalf("served %s: %s", target, rec.Body.String())
			}
			if rec.Code == http.StatusNotFound {
				wantProblem(t, rec, http.StatusNotFound, problemNotFound)
			}
		})
	}
}

func TestSyncProgressSaysIndexingIsOffWithoutIt(t *testing.T) {
	// Given a server started without indexing (--no-sync)
	r := newRig(t)

	// When the page asks where indexing stands
	got := decode[syncStatus](t, r.get("/api/sync"))

	// Then it is told that indexing is off
	if got.State != syncDisabled {
		t.Errorf("without sync: %+v", got)
	}
}

func TestSyncProgressShowsTheFilesIndexedWhileRunningThenWhatWasSkipped(t *testing.T) {
	// Given a server whose indexing is blocked after 12 files
	release := make(chan struct{})
	progressed := make(chan struct{})
	r := newRig(t, func(o *Options) {
		o.Sync = func(_ context.Context, progress func(int)) (string, error) {
			progress(12)
			close(progressed)
			<-release
			return "skipped files: 1", nil
		}
	})
	done := make(chan struct{})
	go func() { r.handler.sync.run(context.Background(), r.handler.opts.Sync); close(done) }()
	<-progressed

	// When the page asks while it runs, then after it ends
	running := decode[syncStatus](t, r.get("/api/sync"))
	close(release)
	<-done
	finished := decode[syncStatus](t, r.get("/api/sync"))

	// Then it sees the progress, then the end with the note about what was skipped
	if running.State != syncRunning || running.Indexed != 12 || running.FinishedAt != 0 {
		t.Errorf("running: %+v", running)
	}
	if finished.State != syncDone || finished.Note != "skipped files: 1" || finished.FinishedAt == 0 {
		t.Errorf("finished: %+v", finished)
	}
}

func TestAFailedIndexingIsReportedWithoutItsCause(t *testing.T) {
	// Given an indexing that fails with a message naming a file
	r := newRig(t, func(o *Options) {
		o.Sync = func(context.Context, func(int)) (string, error) {
			return "", errors.New("read /Users/someone/.claude/projects/x.jsonl: boom")
		}
	})

	// When it ends and the page asks
	r.handler.sync.run(context.Background(), r.handler.opts.Sync)
	rec := r.get("/api/sync")

	// Then the page learns it failed, the cause stays in the log
	got := decode[syncStatus](t, rec)
	if got.State != syncFailed || strings.Contains(rec.Body.String(), "someone") {
		t.Errorf("status = %s", rec.Body.String())
	}
	if len(r.logLines) != 1 || !strings.Contains(r.logLines[0], "boom") {
		t.Errorf("log = %v", r.logLines)
	}
}

// serving starts the server on an ephemeral loopback port and returns its address, a way to end it and what Serve returns.
func serving(t *testing.T, mutate ...func(*Options)) (addr string, stop context.CancelFunc, ended <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := newRig(t, mutate...)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, r.handler.opts) }()
	t.Cleanup(stop)
	return ln.Addr().String(), stop, done
}

func status(t *testing.T, addr, host string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/meta", nil)
	if host != "" {
		req.Host = host
	}
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode
}

func TestServeAnswersOnItsListener(t *testing.T) {
	// Given a server on an ephemeral loopback port
	addr, _, _ := serving(t)

	// When a client asks for the API under the address it connected to
	got := status(t, addr, "")

	// Then it is served
	if got != http.StatusOK {
		t.Errorf("status = %d", got)
	}
}

func TestServeRefusesARebindingHostOnItsListener(t *testing.T) {
	// Given a server on an ephemeral loopback port
	addr, _, _ := serving(t)

	// When a client reaches it under another name, as a DNS rebinding page would
	got := status(t, addr, "evil.example:1")

	// Then it is refused
	if got != http.StatusForbidden {
		t.Errorf("status = %d", got)
	}
}

func TestServeReturnsCleanlyWhenItsContextEnds(t *testing.T) {
	// Given a running server
	addr, stop, ended := serving(t)
	if status(t, addr, "") != http.StatusOK {
		t.Fatal("the server does not answer")
	}

	// When its context is cancelled
	stop()

	// Then Serve returns without an error
	select {
	case err := <-ended:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after its context ended")
	}
}

func TestServeStopsTheIndexingAndWaitsForItWhenItsContextEnds(t *testing.T) {
	// Given a running server whose indexing only ends when it is told to stop
	started := make(chan struct{})
	var stopped atomic.Bool
	addr, stop, ended := serving(t, func(o *Options) {
		o.Sync = func(ctx context.Context, _ func(int)) (string, error) {
			close(started)
			<-ctx.Done()
			time.Sleep(50 * time.Millisecond) // winding down takes a moment, and Serve must wait for it
			stopped.Store(true)
			return "", ctx.Err()
		}
	})
	<-started
	if status(t, addr, "") != http.StatusOK {
		t.Fatal("the server does not answer while indexing")
	}

	// When the server's context is cancelled, as a SIGINT does
	stop()

	// Then Serve returns without an error, and only once the indexing has stopped
	select {
	case err := <-ended:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
		if !stopped.Load() {
			t.Error("Serve returned while the indexing was still running")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after its context ended")
	}
}

func TestAnIndexingStoppedByTheShutdownIsNotLoggedAsAFailure(t *testing.T) {
	// Given an indexing that ends because its context was cancelled
	r := newRig(t, func(o *Options) {
		o.Sync = func(ctx context.Context, _ func(int)) (string, error) { return "", ctx.Err() }
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When it ends
	r.handler.sync.run(ctx, r.handler.opts.Sync)

	// Then the log does not call a stop requested by the user a failure
	if len(r.logLines) != 0 {
		t.Errorf("log = %v", r.logLines)
	}
}

func TestListenAddressesMustBeLoopbackUnlessRemoteIsAllowed(t *testing.T) {
	for _, c := range []struct {
		addr   string
		remote bool
		ok     bool
	}{
		{"127.0.0.1:7373", false, true},
		{"[::1]:7373", false, true},
		{"localhost:7373", false, true},
		{"127.0.0.1:0", false, true},
		{"0.0.0.0:7373", false, false},
		{":7373", false, false},
		{"192.168.1.20:7373", false, false},
		{"[::]:7373", false, false},
		{"example.com:7373", false, false},
		{"7373", false, false},
		{"127.0.0.1", false, false},
		{"0.0.0.0:7373", true, true},
		{":7373", true, true},
	} {
		t.Run(fmt.Sprintf("%s remote=%v", c.addr, c.remote), func(t *testing.T) {
			// Given a listen address, with or without --allow-remote
			// When it is validated
			err := ValidateAddr(c.addr, c.remote)

			// Then only loopback addresses pass without --allow-remote, and the refusal names the risk
			if (err == nil) != c.ok {
				t.Errorf("ValidateAddr(%q, %v) = %v", c.addr, c.remote, err)
			}
			if err != nil && errors.Is(err, ErrRemoteAddr) && !strings.Contains(err.Error(), "--allow-remote") {
				t.Errorf("the refusal does not mention --allow-remote: %v", err)
			}
		})
	}
}
