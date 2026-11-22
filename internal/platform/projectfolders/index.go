package projectfolders

import (
	"database/sql"
	"encoding/json"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Querier is what a database and a transaction on it both answer queries through.
type Querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// Folders returns the working folders of the index, as it holds them, that lie in p. Those are read from the index's
// published usage_folders view and each is resolved before it is compared with the project.
func Folders(q Querier, p kernel.Project) ([]string, error) {
	rows, err := q.Query(`SELECT folder FROM usage_folders`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	within := []string{}
	r := newResolver()
	for rows.Next() {
		var folder string
		if err := rows.Scan(&folder); err != nil {
			return nil, err
		}
		if p.Contains(r.resolve(folder)) {
			within = append(within, folder)
		}
	}
	return within, rows.Err()
}

// List is Folders as one JSON array, the single bound value of an InList condition: the folders travel together, whatever
// their number. It is empty for the zero Project.
func List(q Querier, p kernel.Project) (string, error) {
	if p.IsZero() {
		return "", nil
	}
	folders, err := Folders(q, p)
	if err != nil {
		return "", err
	}
	list, err := json.Marshal(folders)
	return string(list), err
}

// InList is the SQL condition keeping the rows whose folder column (a name written by the caller, never read from input)
// is one of the folders List returned; the list is its one argument.
func InList(column string) string { return column + " IN (SELECT value FROM json_each(?))" }

// Condition is an SQL condition keeping the rows whose folder column holds a folder of project p, and its argument.
// For the zero Project it is empty, which keeps every row.
func Condition(q Querier, p kernel.Project, column string) (cond string, args []any, err error) {
	list, err := List(q, p)
	if err != nil || p.IsZero() {
		return "", nil, err
	}
	return InList(column), []any{list}, nil
}
