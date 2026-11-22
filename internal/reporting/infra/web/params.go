package web

import (
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

// earliestSince is the oldest period start accepted: no agent log is older, and it keeps date arithmetic far from overflow.
var earliestSince = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// params are the validated query parameters of a request.
type params struct {
	query reporting.Query
	limit int
}

// The query parameters a route may accept.
const (
	paramSince   = "since"
	paramHarness = "harness"
	paramLimit   = "limit"
)

// parseParams validates the query string against the parameters the route accepts, none for a route that takes
// none. Unknown and repeated parameters are refused, so that a typo never silently widens a report.
func (s *Server) parseParams(r *http.Request, accepts ...string) (params, *problem) {
	p := params{limit: defaultLimit}
	if len(r.URL.RawQuery) > maxRawQuery {
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
		if len(v) > maxSinceChars {
			pr := invalidParameter(paramSince, "too long")
			return p, &pr
		}
		now := s.now()
		since, err := s.parseSince(v, now)
		if err != nil || since.Before(earliestSince) || since.After(now) {
			pr := invalidParameter(paramSince, "use 24h, 7d, 30d, another duration such as 12h, or a date such as 2006-01-02, not in the future")
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
	if v := values.Get(paramLimit); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			pr := invalidParameter(paramLimit, fmt.Sprintf("an integer from 1 to %d", maxLimit))
			return p, &pr
		}
		p.limit = n
	}
	return p, nil
}
