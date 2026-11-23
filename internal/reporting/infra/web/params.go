package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// Bounds on what a request may ask for.
const (
	maxRawQuery   = 256 // bytes of query string
	maxSinceChars = 32
	defaultLimit  = 100
	maxLimit      = 1000
)

// ValidateSince reads a period start with parse and checks it is one a report can cover (reporting.CheckSince). The API applies it to every request and the command line to --since, so that a period the
// server opens on is a period the API answers.
func ValidateSince(v string, now time.Time, parse func(string, time.Time) (time.Time, error)) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if len(v) <= maxSinceChars && parse != nil {
		if since, err := parse(v, now); err == nil && reporting.CheckSince(since, now) == nil {
			return since, nil
		}
	}
	return time.Time{}, errors.New("use 24h, 7d, 30d, month, another duration such as 12h, or a date such as 2006-01-02, not in the future")
}

// params are the validated query parameters of a request.
type params struct {
	query  reporting.Query
	scope  string // the scope the project of the query stands for: scopeCurrent or scopeAll
	limit  int
	offset int
}

// The query parameters a route may accept.
const (
	paramSince   = "since"
	paramHarness = "harness"
	paramLimit   = "limit"
	paramOffset  = "offset"
	paramProject = "project"
)

// The scopes the project parameter names. The server decides which folder "current" is: a client never sends one.
const (
	scopeCurrent = "current"
	scopeAll     = "all"
)

// defaultScope is the scope the page opens on: the project of the folder the server runs in, unless it was started
// for every project or no project was detected.
func (s *Server) defaultScope() string {
	if project, restricted := s.coverage(); restricted && !project.IsZero() {
		return scopeCurrent
	}
	return scopeAll
}

// scopeProject is the project a scope restricts a query to: the zero Project for every project.
func (s *Server) scopeProject(scope string) (kernel.Project, *problem) {
	project, _ := s.coverage()
	switch {
	case scope == scopeAll:
		return kernel.Project{}, nil
	case scope == scopeCurrent && !project.IsZero():
		return project, nil
	case scope == scopeCurrent:
		pr := invalidParameter(paramProject, "no project was detected: use all")
		return kernel.Project{}, &pr
	}
	pr := invalidParameter(paramProject, "use current or all")
	return kernel.Project{}, &pr
}

// parseParams validates the query string against the parameters the route accepts, none for a route that takes
// none. Unknown and repeated parameters are refused, so that a typo never silently widens a report.
func (s *Server) parseParams(r *http.Request, accepts ...string) (params, *problem) {
	return s.parseParamsBounded(r, maxRawQuery, accepts...)
}

func (s *Server) parseParamsBounded(r *http.Request, maxQuery int, accepts ...string) (params, *problem) {
	p := params{limit: defaultLimit}
	if len(r.URL.RawQuery) > maxQuery {
		pr := problem{Type: problemInvalidParameter, Title: "Query string too long", Status: http.StatusRequestURITooLong}
		return p, &pr
	}
	// Not r.URL.Query(): it drops the pairs it cannot parse, and a broken filter would silently widen the report.
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		pr := problem{Type: problemInvalidParameter, Title: "Malformed query string", Status: http.StatusBadRequest, Detail: "the query string is not valid URL-encoded pairs"}
		return p, &pr
	}
	for name, vs := range values {
		if !slices.Contains(accepts, name) {
			pr := invalidParameter(name, "unknown parameter")
			return p, &pr
		}
		if len(vs) != 1 {
			pr := invalidParameter(name, "give it once")
			return p, &pr
		}
	}
	if v := values.Get(paramSince); v != "" {
		since, err := ValidateSince(v, s.now(), s.parseSince)
		if err != nil {
			pr := invalidParameter(paramSince, err.Error())
			return p, &pr
		}
		p.query.Since = since
	}
	if v := values.Get(paramHarness); v != "" {
		h, err := kernel.ParseHarness(v)
		if err != nil {
			pr := invalidParameter(paramHarness, err.Error())
			return p, &pr
		}
		p.query.Harness = h
	}
	scope := s.defaultScope()
	if values.Has(paramProject) { // an empty value is refused, not read as the default
		scope = values.Get(paramProject)
	}
	project, bad := s.scopeProject(scope)
	if bad != nil {
		return p, bad
	}
	p.query.Project, p.scope = project, scope
	if v := values.Get(paramLimit); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			pr := invalidParameter(paramLimit, fmt.Sprintf("an integer from 1 to %d", maxLimit))
			return p, &pr
		}
		p.limit = n
	}
	if values.Has(paramOffset) {
		n, err := strconv.ParseInt(values.Get(paramOffset), 10, 32)
		if err != nil || n < 0 {
			pr := invalidParameter(paramOffset, "use a non-negative integer")
			return p, &pr
		}
		p.offset = int(n)
	}
	return p, nil
}
