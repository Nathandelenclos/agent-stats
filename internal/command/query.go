package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/pricing/infra/pricetable"
	"github.com/Nathandelenclos/agent-stats/internal/quota"
	usagedb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/infra/web"
)

type queryRoute struct {
	path  string
	flags []string
}

var queryRoutes = map[string]queryRoute{
	"overview":         {"/api/report", nil},
	"models":           {"/api/activity-series", nil},
	"tools":            {"/api/activity-series", nil},
	"tool-models":      {"/api/tool-models", nil},
	"sessions":         {"/api/sessions", []string{"limit", "offset"}},
	"session":          {"/api/sessions/{session}", []string{"session", "excerpts"}},
	"related-sessions": {"/api/related-sessions", []string{"kind", "value", "server", "item-harness", "item-scope", "item-origin", "limit", "offset"}},
	"requests":         {"/api/requests", []string{"session", "model", "provider", "failed", "signature", "q", "limit", "offset"}},
	"errors":           {"/api/errors", []string{"limit"}},
	"trace":            {"/api/sessions/{session}/trace", []string{"session", "limit", "offset"}},
	"prompts":          {"/api/prompts", []string{"limit", "offset", "excerpts"}},
	"prompt-signals":   {"/api/prompts/model-signals", nil},
	"prompt":           {"/api/sessions/{session}/prompts/{position}", []string{"session", "position", "revision"}},
	"catalog":          {"/api/catalog", []string{"unused"}},
	"quotas":           {"", []string{"limit", "offset"}},
}

var queryFlagHelp = map[string]string{
	"limit": "number of rows to return (1 to 1000)", "offset": "number of rows to skip",
	"session": "exact indexed session ID", "position": "indexed prompt position", "revision": "revision of the prompt preview",
	"model": "exact model name", "provider": "exact provider for --model (empty means unknown)",
	"failed": "only failed requests", "signature": "error signature from the errors dataset", "q": "search requests",
	"kind": "relation kind: day, harness, model, folder, agent, tool or mcp", "value": "relation value",
	"server": "MCP server name for a tool relation", "item-harness": "harness of a catalog MCP entry",
	"item-scope": "scope of a catalog MCP entry", "item-origin": "origin ID of a catalog MCP entry",
	"unused": "only enabled catalog entries with zero observed use",
}

type queryEnvelope struct {
	SchemaVersion int             `json:"schemaVersion"`
	Dataset       string          `json:"dataset"`
	Scope         string          `json:"scope"`
	Project       string          `json:"project,omitempty"`
	Harness       string          `json:"harness,omitempty"`
	Since         string          `json:"since,omitempty"`
	Data          json.RawMessage `json:"data"`
}

func queryUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-stats query <dataset> [flags]")
	datasets := make([]string, 0, len(queryRoutes))
	for name := range queryRoutes {
		datasets = append(datasets, name)
	}
	slices.Sort(datasets)
	fmt.Fprintln(w, "Datasets:", strings.Join(datasets, ", "))
	fmt.Fprintln(w, "Run agent-stats query <dataset> --help for dataset filters.")
}

func runQuery(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		queryUsage(stderr)
		return errors.New("query requires a dataset")
	}
	dataset := args[0]
	if dataset == "--help" || dataset == "-h" {
		queryUsage(stdout)
		return nil
	}
	route, ok := queryRoutes[dataset]
	if !ok {
		queryUsage(stderr)
		return fmt.Errorf("unknown query dataset %q", dataset)
	}

	fs := flag.NewFlagSet("agent-stats query "+dataset, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", defaultIndexPath(), "path to the existing SQLite index")
	since := fs.String("since", "", "period start: 24h, 7d, a date (YYYY-MM-DD), or all")
	asJSON := fs.Bool("json", true, "print the JSON result")
	var all, excerpts bool
	var project, harness, pricesPath string
	if dataset != "quotas" {
		fs.BoolVar(&all, "all", false, "include all projects")
		fs.StringVar(&project, "project", "", "project folder (defaults to the current project)")
		fs.StringVar(&harness, "harness", "", "restrict to one harness")
	}
	if dataset == "overview" {
		fs.StringVar(&pricesPath, "prices", "", "price table for the cost breakdown")
	}
	for _, name := range route.flags {
		switch name {
		case "failed", "unused":
			fs.Bool(name, false, queryFlagHelp[name])
		case "excerpts":
			fs.BoolVar(&excerpts, name, false, "include prompt excerpts")
		default:
			fs.String(name, "", queryFlagHelp[name])
		}
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected query argument %q", fs.Arg(0))
	}
	if !*asJSON {
		return errors.New("query output is JSON")
	}
	if *dbPath == "" {
		return errors.New("no user cache directory: pass --db")
	}

	now := time.Now()
	period := *since
	if period == "all" {
		period = ""
	}
	from, err := web.ValidateSince(period, now, parseSince)
	if err != nil {
		return fmt.Errorf("invalid --since: %w", err)
	}
	result := queryEnvelope{SchemaVersion: 1, Dataset: dataset, Harness: harness, Since: from.Format(time.RFC3339Nano)}
	if from.IsZero() {
		result.Since = "all"
	}
	values := queryValues(fs, route.flags)
	if dataset == "quotas" {
		result.Scope = "account"
		if result.Data, err = queryQuotas(ctx, *dbPath, from, values); err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(result)
	}

	cov, err := queryCoverage(all, project)
	if err != nil {
		return err
	}
	result.Scope = "all"
	if cov.restricted {
		result.Scope, result.Project = "project", cov.project.Root()
	}
	if period != "" {
		values.Set("since", period)
	}
	if harness != "" {
		values.Set("harness", harness)
	}
	path, err := queryPath(dataset, route.path, values)
	if err != nil {
		return err
	}
	usage, err := usagedb.OpenReadOnly(*dbPath)
	if err != nil {
		return fmt.Errorf("open existing index: %w", err)
	}
	defer usage.Close()
	prices, err := pricetable.Load(pricesPath)
	if err != nil {
		return err
	}
	opts := reportingOptions(usage, prices, cov)
	opts.Now = func() time.Time { return now }
	opts.Log = log.New(stderr, "agent-stats query: ", 0)
	opts.Catalog = interactiveCatalog(*dbPath, project)
	opts.OmitPromptExcerpts = !excerpts
	result.Data, err = serveQuery(ctx, opts, path, values)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(result)
}

func queryCoverage(all bool, name string) (coverage, error) {
	if all {
		if name != "" {
			return coverage{}, errors.New("choose --all or --project")
		}
		return coverage{}, nil
	}
	project := workingProject()
	if name != "" {
		var err error
		if project, err = givenProject(name); err != nil {
			return coverage{}, err
		}
	}
	project, _ = kernel.NamedProject(project, homeProject())
	if project.IsZero() {
		return coverage{}, errors.New("choose a project with --project or explicitly request --all")
	}
	return coverage{project: project, restricted: true}, nil
}

func queryValues(fs *flag.FlagSet, names []string) url.Values {
	values := make(url.Values)
	for _, name := range names {
		value := fs.Lookup(name).Value.String()
		switch name {
		case "excerpts":
			continue
		case "failed":
			if value == "true" {
				values.Set(name, "1")
			}
		case "unused":
			if value == "true" {
				values.Set(name, value)
			}
		default:
			if value != "" {
				values.Set(name, value)
			}
		}
	}
	for flagName, parameter := range map[string]string{"item-harness": "itemHarness", "item-scope": "itemScope", "item-origin": "itemOrigin"} {
		if value := values.Get(flagName); value != "" {
			values.Set(parameter, value)
			values.Del(flagName)
		}
	}
	return values
}

func queryPath(dataset, path string, values url.Values) (string, error) {
	id := values.Get("session")
	values.Del("session")
	if strings.Contains(path, "{session}") && id == "" {
		return "", fmt.Errorf("query %s requires --session", dataset)
	}
	if id == "." || id == ".." {
		return "", errors.New("invalid session ID")
	}
	if id != "" && dataset == "requests" {
		path = "/api/sessions/{session}/requests"
	}
	path = strings.ReplaceAll(path, "{session}", url.PathEscape(id))
	if dataset == "prompt" {
		n, err := strconv.ParseInt(values.Get("position"), 10, 64)
		if err != nil || n < 1 {
			return "", errors.New("query prompt requires a positive --position")
		}
		path = strings.ReplaceAll(path, "{position}", strconv.FormatInt(n, 10))
		values.Del("position")
	}
	if dataset == "models" || dataset == "tools" {
		values.Set("kind", dataset)
	}
	return path, nil
}

// Route through the dashboard's existing read handlers in process, with no listener.
func serveQuery(ctx context.Context, opts web.Options, path string, values url.Values) (json.RawMessage, error) {
	address := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
	handler := web.New(opts, address)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address.String()+path+"?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	body := recorder.Body.Bytes()
	if recorder.Code != http.StatusOK {
		var failure struct{ Title, Parameter, Detail string }
		if json.Unmarshal(body, &failure) == nil && failure.Detail != "" {
			if failure.Parameter != "" {
				return nil, fmt.Errorf("%s: %s", failure.Parameter, failure.Detail)
			}
			return nil, errors.New(failure.Detail)
		}
		if failure.Title != "" {
			return nil, errors.New(failure.Title)
		}
		return nil, fmt.Errorf("query failed (status %d)", recorder.Code)
	}
	return json.RawMessage(body), nil
}

func queryQuotas(ctx context.Context, dbPath string, from time.Time, values url.Values) (json.RawMessage, error) {
	limit, offset := 100, 0
	if text := values.Get("limit"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > 1000 {
			return nil, errors.New("use --limit from 1 to 1000")
		}
		limit = n
	}
	if text := values.Get("offset"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 {
			return nil, errors.New("use a non-negative --offset")
		}
		offset = n
	}
	windows, err := sqlite.ReadQuotaHistory(ctx, dbPath, from, limit+1, offset)
	if err != nil {
		return nil, fmt.Errorf("read recorded quotas: %w", err)
	}
	return json.Marshal(struct {
		Windows []quota.Window `json:"windows"`
		HasMore bool           `json:"hasMore"`
	}{append([]quota.Window{}, windows[:min(len(windows), limit)]...), len(windows) > limit})
}
