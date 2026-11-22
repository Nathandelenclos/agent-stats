package logfiles

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// OpenCode and Kilo Code keep every session of the machine in one SQLite database. Two generations of its schema
// exist and a database may hold both:
//   - messages: tables session, message and part, whose data columns hold the JSON of the message and of its parts;
//   - events:   tables session_v2 (or session), session_message and session_part, whose data columns hold the
//     assistant message with the first-token and end-of-stream times.
//
// The database is opened read-only and never written, locked for writing or copied: the session is the unit the
// index keeps, so List turns each session into a log of its own named "<database>#<session id>", and a log
// changes when its session does (see kindStats.revision).

// DBSessionSep separates the database path from the session id in the path of a session read from a database.
const DBSessionSep = "#"

// A row's data over this many bytes is not read: a database is as untrusted as a log file is.
var maxDBRowBytes = maxLineBytes

// maxDBSessionBytes bounds what one session may hold in memory, in bytes of data read.
var maxDBSessionBytes = maxLogBytes

// ErrUnknownSchema reports a database whose tables are none of the schemas this reader knows.
var ErrUnknownSchema = errors.New("unrecognised database schema")

// ErrDatabaseUnreadable reports a database, or a session in it, that could not be read: what the SQLite driver says
// is kept as text, so that nothing above this reader depends on the driver's errors.
var ErrDatabaseUnreadable = errors.New("session database unreadable")

// ErrSessionGone reports a session that was listed and is not in its database any more.
var ErrSessionGone = errors.New("session no longer in its database")

// unreadable turns what the driver reports into ErrDatabaseUnreadable, and keeps the errors that are this reader's own.
func unreadable(subject string, err error) error {
	switch {
	case errors.Is(err, ErrUnknownSchema), errors.Is(err, ErrNotRegular), errors.Is(err, ErrLogTooLarge):
		return fmt.Errorf("%s: %w", subject, err)
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%s: %w", subject, ErrSessionGone)
	}
	return fmt.Errorf("%s: %w: %v", subject, ErrDatabaseUnreadable, err)
}

// OpenCodeDatabase is the reader of the databases of one harness of the OpenCode family.
// Prefix names the database files in the data directory ("opencode" for opencode.db and opencode-<channel>.db),
// Override is the path the user set in OPENCODE_DB or KILO_DB, absolute or relative to the data directory.
type OpenCodeDatabase struct {
	Prefix   string
	Override string
	// Fallback is the old prefix of the channel databases, read only where the channel has no database under Prefix:
	// Kilo used to name them like OpenCode does.
	Fallback string

	mu      sync.Mutex
	handles map[string]*sql.DB
	schemas map[string]dbSchema
}

// dbSession is a session found in a database, with what tells that it changed.
type dbSession struct {
	id     string
	table  string // the session table that holds it
	events bool   // true when its messages are in session_message, false in message
	// revision says when the session changed (see kindStats.revision), and weight how many bytes of data it holds.
	revision string
	weight   int64
}

// dbSchema is what a database holds: the session tables and message tables that have the columns this reader needs.
type dbSchema struct {
	sessionTables []string
	messages      bool            // message and part
	events        bool            // session_message
	parts         bool            // session_part
	stamped       map[string]bool // the tables of parts that say when a row was updated
	forks         map[string]bool // the session tables that say which session a session was forked from
}

var sessionTableColumns = []string{"id", "parent_id", "directory", "title", "time_created", "time_updated"}

// paths lists the databases to read under dir. A database is a regular file named <prefix>.db or <prefix>-<channel>.db.
func (d *OpenCodeDatabase) paths(dir string) []string {
	if d.Override != "" && d.Override != ":memory:" {
		p := d.Override
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		return []string{p}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := map[string]bool{}
	for _, e := range entries {
		if e.Type().IsRegular() || e.Type()&os.ModeSymlink != 0 {
			names[e.Name()] = true
		}
	}
	var out []string
	for name := range names {
		if _, ok := channelOf(name, d.Prefix); ok {
			out = append(out, filepath.Join(dir, name))
		} else if channel, ok := channelOf(name, d.Fallback); ok && channel != "" && !names[d.Prefix+"-"+channel+".db"] {
			out = append(out, filepath.Join(dir, name))
		}
	}
	slices.Sort(out)
	return out
}

// channelOf matches "<prefix>.db" (empty channel) and "<prefix>-<channel>.db".
func channelOf(name, prefix string) (channel string, ok bool) {
	if prefix == "" || !strings.HasSuffix(name, ".db") {
		return "", false
	}
	stem := strings.TrimSuffix(name, ".db")
	if stem == prefix {
		return "", true
	}
	if c, ok := strings.CutPrefix(stem, prefix+"-"); ok && c != "" {
		return c, true
	}
	return "", false
}

// connect opens the read-only connection pool of a database for a listing, replacing the one of the previous
// listing. The file is checked first: opening a FIFO or a device by name would block.
//
// The database is opened with mode=ro, never immutable: no -wal or -shm file beside a database does not mean that
// nobody writes to it, and an immutable connection would neither see what a harness that starts later commits nor
// notice that the file changed under it. mode=ro never writes the database. For a database in write-ahead mode
// SQLite may create or need the -shm and -wal side files, so the directory must be writable (see the README).
func (d *OpenCodeDatabase) connect(path string) (*sql.DB, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if old, ok := d.handles[path]; ok {
		old.Close()
		delete(d.handles, path)
	}
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	f.Close()
	uri := (&url.URL{Scheme: "file", Path: path}).String() + "?mode=ro&_pragma=query_only(true)&_pragma=busy_timeout(3000)"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	if d.handles == nil {
		d.handles, d.schemas = map[string]*sql.DB{}, map[string]dbSchema{}
	}
	d.handles[path] = db
	return db, nil
}

// handle returns the pool a listing opened.
func (d *OpenCodeDatabase) handle(path string) (*sql.DB, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if db, ok := d.handles[path]; ok {
		return db, nil
	}
	return nil, fmt.Errorf("%s was not listed", path)
}

func columns(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table string) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `PRAGMA table_info(`+table+`)`) // table is one of the names below, never a value of the database
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func hasAll(cols map[string]bool, want ...string) bool {
	for _, w := range want {
		if !cols[w] {
			return false
		}
	}
	return len(cols) > 0
}

// detect reads which tables the database has. A database that has none of the known layouts is refused.
func (d *OpenCodeDatabase) detect(ctx context.Context, db *sql.DB) (dbSchema, error) {
	s := dbSchema{forks: map[string]bool{}}
	for _, t := range []string{"session_v2", "session"} {
		c, err := columns(ctx, db, t)
		if err != nil {
			return s, err
		}
		if hasAll(c, sessionTableColumns...) {
			s.sessionTables = append(s.sessionTables, t)
			s.forks[t] = c["fork_session_id"]
		}
	}
	c, err := columns(ctx, db, "message")
	if err != nil {
		return s, err
	}
	p, err := columns(ctx, db, "part")
	if err != nil {
		return s, err
	}
	s.stamped = map[string]bool{"part": p["time_updated"]}
	s.messages = hasAll(c, "id", "session_id", "time_created", "time_updated", "data") &&
		hasAll(p, "id", "message_id", "session_id", "data")
	e, err := columns(ctx, db, "session_message")
	if err != nil {
		return s, err
	}
	s.events = hasAll(e, "id", "session_id", "type", "time_created", "time_updated", "data")
	sp, err := columns(ctx, db, "session_part")
	if err != nil {
		return s, err
	}
	s.stamped["session_part"] = sp["time_updated"]
	s.parts = hasAll(sp, "message_id", "session_id", "ordinal", "type", "data")
	if len(s.sessionTables) == 0 || !(s.messages || s.events) {
		return s, ErrUnknownSchema
	}
	return s, nil
}

// forkedTitle is how OpenCode names a fork of a session of the older generation of the schema, which does not say
// what a session was forked from: "<title> (fork #2)".
var forkedTitle = regexp.MustCompile(`\(fork #[0-9]+\)$`)

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)

// stats is what a table holds of one session: how many rows, the sum of the times they were updated and how many bytes of data.
type stats struct{ rows, updated, bytes int64 }

func (s stats) String() string { return fmt.Sprintf("%d.%d.%d", s.rows, s.updated, s.bytes) }

// tableStats counts, per session, the rows of a table of messages or of parts. The bytes of data catch what the
// count misses: a part that goes from running to error keeps its row. The times are summed, not maximised: a row
// stamped in the future would hide every later update from a maximum. Each is bounded (year 2100) so that the sum
// cannot overflow. A table that does not state when a row was updated reports 0.
// ponytail: this reads the data of every row at each listing; keep a fingerprint per session in the index if a
// database ever makes that slow.
func tableStats(ctx context.Context, db *sql.DB, table string, stamped bool) (map[string]stats, error) {
	updated := "0"
	if stamped {
		updated = "SUM(MIN(MAX(time_updated, 0), 4102444800000))"
	}
	rows, err := db.QueryContext(ctx, `SELECT session_id, COUNT(*), `+updated+`, COALESCE(SUM(length(CAST(data AS BLOB))), 0) FROM `+table+` GROUP BY session_id`) // table is one of the names of the schema, never a value of the database
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]stats{}
	for rows.Next() {
		var id string
		var s stats
		if err := rows.Scan(&id, &s.rows, &s.updated, &s.bytes); err != nil {
			return nil, err
		}
		out[id] = s
	}
	return out, rows.Err()
}

// kindStats is what the tables of one generation of the schema say of the sessions: messages and parts.
type kindStats struct{ messages, parts map[string]stats }

func kindStatsOf(ctx context.Context, db *sql.DB, s dbSchema, events bool) (k kindStats, err error) {
	messages, parts := "message", "part"
	if events {
		messages, parts = "session_message", "session_part"
	}
	if k.messages, err = tableStats(ctx, db, messages, true); err != nil {
		return k, err
	}
	if events && !s.parts {
		return k, nil
	}
	k.parts, err = tableStats(ctx, db, parts, s.stamped[parts])
	return k, err
}

// revision says when a session changed, from what its tables hold of it: when the session was updated, and for
// its messages and for its parts the number of rows, the last update and the bytes of data. The size and
// modification time of the database file would not do, as they change whenever any session is written, and every
// session would be read again. Reading through SQLite also takes the -wal file into account.
// weight is the bytes of data the session holds.
func (k kindStats) revision(sessionUpdated int64, events bool, id string) (revision string, weight int64) {
	m, p := k.messages[id], k.parts[id]
	kind := "m"
	if events {
		kind = "e"
	}
	return fmt.Sprintf("%s:%d:%s:%s", kind, sessionUpdated, m, p), m.bytes + p.bytes
}

// list returns the sessions of a database, and the problems met on the way. A database that is busy or in a
// schema this reader does not know is a problem, not a failure. A session with no message is listed too: one that
// lost its last message must be read again, to be empty.
func (d *OpenCodeDatabase) list(path string) (sessions []dbSession, problems []error) {
	ctx := context.Background()
	db, err := d.connect(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil // the harness is not installed, or has not created its database yet
	}
	if err != nil {
		return nil, []error{unreadable(path, err)}
	}
	schema, err := d.detect(ctx, db)
	if err != nil {
		return nil, []error{unreadable(path, err)}
	}
	d.mu.Lock()
	d.schemas[path] = schema
	d.mu.Unlock()
	type generation struct {
		events bool
		stats  kindStats
	}
	var generations []generation // the newer one first: its messages win over those of the older one
	for _, events := range []bool{true, false} {
		if (events && !schema.events) || (!events && !schema.messages) {
			continue
		}
		k, err := kindStatsOf(ctx, db, schema, events)
		if err != nil {
			return nil, []error{unreadable(path, err)}
		}
		generations = append(generations, generation{events, k})
	}
	found, rowsOf := map[string]dbSession{}, map[string]int64{}
	for _, t := range schema.sessionTables {
		rows, err := db.QueryContext(ctx, `SELECT id, time_updated FROM `+t) // t is one of the names above
		if err != nil {
			problems = append(problems, unreadable(path, err))
			continue
		}
		for rows.Next() {
			var id string
			var updated int64
			if err := rows.Scan(&id, &updated); err != nil {
				problems = append(problems, unreadable(path, err))
				break
			}
			if !sessionIDPattern.MatchString(id) {
				problems = append(problems, fmt.Errorf("%s: session with an unusable id", path))
				continue
			}
			// Of its messages in the newer generation and in the older, a session is read from the first that has some
			// (from the newer one when it has none), and from the first table that holds it.
			for _, g := range generations {
				revision, weight := g.stats.revision(updated, g.events, id)
				n := g.stats.messages[id].rows
				if _, ok := found[id]; !ok || (n > 0 && rowsOf[id] == 0) {
					found[id], rowsOf[id] = dbSession{id: id, table: t, events: g.events, revision: revision, weight: weight}, n
				}
			}
		}
		if err := rows.Err(); err != nil {
			problems = append(problems, unreadable(path, err))
		}
		rows.Close()
	}
	for _, s := range found {
		sessions = append(sessions, s)
	}
	slices.SortFunc(sessions, func(a, b dbSession) int { return strings.Compare(a.id, b.id) })
	return sessions, problems
}

// epochMilli reads a time a database states in milliseconds; the zero time when it is not one.
func epochMilli(ms int64) time.Time {
	if ms <= 0 || ms > 253402300799000 { // year 9999
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

type dbTokens struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Cache     struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

// dbMessage is a message of either generation: the fields that differ are told apart by name.
type dbMessage struct {
	Role       string                                    `json:"role"`
	Type       string                                    `json:"type"`
	Text       string                                    `json:"text"`
	Status     string                                    `json:"status"`
	ModelID    string                                    `json:"modelID"`
	ProviderID string                                    `json:"providerID"`
	Model      *struct{ ID, ProviderID, ModelID string } `json:"model"`
	Cost       float64                                   `json:"cost"`
	Tokens     *dbTokens                                 `json:"tokens"`
	Finish     string                                    `json:"finish"`
	Error      *struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"error"`
	Time struct {
		Created    float64 `json:"created"`
		FirstToken float64 `json:"firstToken"`
		Streamed   float64 `json:"streamed"`
		Completed  float64 `json:"completed"`
	} `json:"time"`
	Content []dbPart `json:"content"`
}

type dbPart struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Synthetic bool   `json:"synthetic"`
	Ignored   bool   `json:"ignored"`
	Tool      string `json:"tool"` // messages
	Name      string `json:"name"` // events
	State     struct {
		Status  string                       `json:"status"`
		Input   size                         `json:"input"`
		Output  size                         `json:"output"`
		Content size                         `json:"content"`
		Error   size                         `json:"error"`
		Time    struct{ Start, End float64 } `json:"time"`
	} `json:"state"`
	Time struct {
		Created   float64 `json:"created"`
		Ran       float64 `json:"ran"`
		Completed float64 `json:"completed"`
	} `json:"time"`
}

func msFloat(ms float64) time.Time {
	if ms <= 0 || ms >= 2.5e14 {
		return time.Time{}
	}
	return time.UnixMilli(int64(ms))
}

// failureName is the kind of error a message states, when it is a plain name.
var failureName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.:-]{0,63}$`)

func (m dbMessage) failure() string {
	if m.Error == nil {
		return ""
	}
	for _, n := range []string{m.Error.Name, m.Error.Type} {
		if failureName.MatchString(n) {
			return n
		}
	}
	return domain.UnspecifiedFailure
}

func (m dbMessage) tokens() (kernel.Tokens, error) {
	t := m.Tokens
	if t == nil {
		return kernel.Tokens{}, nil
	}
	return kernel.NewTokens(kernel.TokenCounts{Input: t.Input, Output: t.Output + t.Reasoning, Reasoning: t.Reasoning,
		CacheRead: t.Cache.Read, CacheWrite: t.Cache.Write})
}

// request makes the model call an assistant message records. A message without usage and without an error is
// one that was still being written, or never was finished: it is not a request. ok is false for it.
func (m dbMessage) request(key string, at time.Time) (req domain.Request, ok bool, err error) {
	failure := m.failure()
	tokens, err := m.tokens()
	if err != nil {
		return req, false, err
	}
	cost, err := kernel.NewCost(m.Cost)
	if err != nil {
		return req, false, err
	}
	if m.Tokens == nil || tokens == (kernel.Tokens{}) {
		if failure == "" && cost.IsZero() {
			return req, false, nil
		}
	}
	model, provider := m.ModelID, m.ProviderID
	if m.Model != nil {
		model, provider = cmp2(m.Model.ID, m.Model.ModelID), cmp2(m.Model.ProviderID, provider)
	}
	req = domain.Request{Key: key, At: at, Model: model, Provider: provider, Tokens: tokens, LoggedCost: cost, StopReason: m.Finish, Failure: failure}
	if first, created := msFloat(m.Time.FirstToken), msFloat(m.Time.Created); !first.IsZero() && !created.IsZero() {
		req.TTFT = between(created, first)
	}
	if end, created := msFloat(m.Time.Streamed), msFloat(m.Time.Created); !end.IsZero() && !created.IsZero() {
		req.Duration = between(created, end)
	}
	return req, true, nil
}

func cmp2(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (p dbPart) toolCall(key string, fallback time.Time) domain.ToolCall {
	name := cmp2(p.Tool, p.Name)
	server, tool := domain.SplitMCPToolName(name)
	start := msFloat(cmp0(p.State.Time.Start, p.Time.Ran, p.Time.Created))
	end := msFloat(cmp0(p.State.Time.End, p.Time.Completed))
	at := start
	if at.IsZero() {
		at = fallback
	}
	result := int(p.State.Output) + int(p.State.Content)
	if p.State.Status == "error" {
		result = int(p.State.Error)
	}
	return domain.ToolCall{Key: key, At: at, Name: tool, Server: server, ArgsChars: int(p.State.Input), ResultChars: result,
		IsError: p.State.Status == "error", Duration: between(start, end)}
}

func cmp0(v ...float64) float64 {
	for _, x := range v {
		if x != 0 {
			return x
		}
	}
	return 0
}

// absoluteFolder keeps a directory a database names when it is an absolute path of a printable kind.
func absoluteFolder(dir string) string {
	if len(dir) > 4096 || strings.IndexFunc(dir, unicode.IsControl) >= 0 {
		return ""
	}
	if strings.HasPrefix(dir, "/") || (len(dir) > 2 && dir[1] == ':' && (dir[2] == '/' || dir[2] == '\\')) {
		return dir
	}
	return ""
}

type dbRow struct {
	id      string
	typ     string
	created time.Time
	data    []byte
}

// read parses one session of a database into a transcript. It reads in one read transaction, so that the
// messages and the parts it joins are those of one moment.
func (d *OpenCodeDatabase) read(path string, h kernel.Harness, s dbSession) (domain.Transcript, error) {
	t, err := d.readSession(path, h, s)
	if err != nil {
		return domain.Transcript{}, unreadable(path+DBSessionSep+s.id, err)
	}
	return t, nil
}

func (d *OpenCodeDatabase) readSession(path string, h kernel.Harness, s dbSession) (domain.Transcript, error) {
	ctx := context.Background()
	db, err := d.handle(path)
	if err != nil {
		return domain.Transcript{}, err
	}
	d.mu.Lock()
	schema := d.schemas[path]
	d.mu.Unlock()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.Transcript{}, err
	}
	defer tx.Rollback()

	p := domain.Transcript{Session: domain.Session{Harness: h, AgentType: domain.MainAgent}}
	if p.Session.ID, err = domain.NewSessionID(h, s.id); err != nil {
		return domain.Transcript{}, err
	}
	var parent, directory, title, forkedFrom sql.NullString
	var created int64
	forkColumn := "NULL"
	if schema.forks[s.table] {
		forkColumn = "fork_session_id"
	}
	err = tx.QueryRowContext(ctx, `SELECT parent_id, directory, title, time_created, `+forkColumn+` FROM `+s.table+` WHERE id = ?`, s.id).
		Scan(&parent, &directory, &title, &created, &forkedFrom)
	if err != nil {
		return domain.Transcript{}, err
	}
	if sessionIDPattern.MatchString(parent.String) {
		if p.Session.ParentID, err = domain.NewSessionID(h, parent.String); err != nil {
			return domain.Transcript{}, err
		}
		p.Session.AgentType = domain.Subagent
	}
	p.Session.Folder = absoluteFolder(directory.String)
	p.Session.Title = domain.Title(title.String)
	sessionStart := epochMilli(created)
	forked := forkedFrom.String != "" || forkedTitle.MatchString(title.String)

	budget := maxDBSessionBytes
	query := func(q string, each func(r dbRow)) error {
		rows, err := tx.QueryContext(ctx, q, maxDBRowBytes, s.id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r dbRow
			var typ sql.NullString
			var at int64
			if err := rows.Scan(&r.id, &typ, &at, &r.data); err != nil {
				return err
			}
			if budget -= int64(len(r.data)); budget < 0 {
				return fmt.Errorf("%w: over %d bytes", ErrLogTooLarge, maxDBSessionBytes)
			}
			r.typ, r.created = typ.String, epochMilli(at)
			each(r)
		}
		return rows.Err()
	}

	type message struct {
		dbMessage
		key    string
		at     time.Time
		prompt []string
	}
	var messages []*message
	byID := map[string]*message{}
	decode := func(r dbRow) (*message, bool) {
		var m message
		if r.data == nil || json.Unmarshal(r.data, &m.dbMessage) != nil {
			p.SkippedLines++ // a row that is not read, or not JSON, or not shaped like a message
			return nil, false
		}
		m.key, m.at = r.id, r.created
		if m.at.IsZero() {
			m.at = msFloat(m.Time.Created)
		}
		// A session forked from another one holds a copy of the messages of the original, which keep the time they
		// were made at in their data, whatever the time the copy was stamped with: those older than the session are
		// history, counted with the session they belong to. A session that is not a fork keeps all its messages.
		if forked && !sessionStart.IsZero() {
			made := m.at
			if t := msFloat(m.Time.Created); !t.IsZero() {
				made = t
			}
			if !made.IsZero() && made.Before(sessionStart) {
				return nil, false
			}
		}
		if r.typ != "" {
			m.Role = r.typ
		}
		return &m, true
	}

	if s.events {
		err = query(`SELECT id, type, time_created, CASE WHEN length(data) > ? THEN NULL ELSE data END FROM session_message WHERE session_id = ? ORDER BY time_created, id`,
			func(r dbRow) {
				if m, ok := decode(r); ok {
					messages = append(messages, m)
					byID[m.key] = m
				}
			})
		if err == nil && schema.parts {
			err = query(`SELECT message_id || ':' || ordinal, type, time_created, CASE WHEN length(data) > ? THEN NULL ELSE data END FROM session_part WHERE session_id = ? ORDER BY message_id, ordinal`,
				func(r dbRow) {
					var part dbPart
					if r.data == nil || json.Unmarshal(r.data, &part) != nil {
						p.SkippedLines++
						return
					}
					if mid, _, ok := strings.Cut(r.id, ":"); ok && part.Type == "tool" {
						if m := byID[mid]; m != nil {
							p.Tools = append(p.Tools, part.toolCall(r.id, m.at))
						}
					}
				})
		}
	} else {
		err = query(`SELECT id, '', time_created, CASE WHEN length(data) > ? THEN NULL ELSE data END FROM message WHERE session_id = ? ORDER BY time_created, id`,
			func(r dbRow) {
				if m, ok := decode(r); ok {
					messages = append(messages, m)
					byID[m.key] = m
				}
			})
		if err == nil {
			err = query(`SELECT id || ':' || message_id, '', 0, CASE WHEN length(data) > ? THEN NULL ELSE data END FROM part WHERE session_id = ? ORDER BY message_id, id`,
				func(r dbRow) {
					var part dbPart
					if r.data == nil || json.Unmarshal(r.data, &part) != nil {
						p.SkippedLines++
						return
					}
					partID, mid, _ := strings.Cut(r.id, ":")
					m := byID[mid]
					switch {
					case m == nil:
					case part.Type == "tool" && m.Role == "assistant":
						p.Tools = append(p.Tools, part.toolCall(partID, m.at))
					case part.Type == "text" && m.Role == "user" && !part.Synthetic && !part.Ignored:
						m.prompt = append(m.prompt, part.Text)
					}
				})
		}
	}
	if err != nil {
		return domain.Transcript{}, err
	}

	for _, m := range messages {
		p.Session.Span(m.at)
		switch {
		case m.Role == "user":
			text := strings.Join(m.prompt, "\n")
			if s.events {
				text = m.Text
			}
			if pr, ok := domain.NewPrompt(m.key, m.at, text); ok {
				p.Prompts = append(p.Prompts, pr)
			}
		case m.Role == "assistant" || (m.Role == "compaction" && (m.Status == "completed" || m.Status == "failed")): // a compaction in progress is not a request yet
			req, ok, err := m.request(m.key, m.at)
			switch {
			case err != nil:
				p.SkippedLines++
			case ok:
				p.Requests = append(p.Requests, req)
			}
			for i, c := range m.Content { // tool calls an events-generation message holds itself
				if c.Type == "tool" {
					p.Tools = append(p.Tools, c.toolCall(fmt.Sprintf("%s:c%d", m.key, i), m.at))
				}
			}
		}
	}
	p.Session.Span(sessionStart)
	p.TitleFromFirstPrompt()
	return p, nil
}
