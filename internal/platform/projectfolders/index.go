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

// Condition is an SQL condition keeping the rows whose folder column (a name written by the caller, never read from
// input) holds a folder of project p, and its argument: the folders travel as one bound value, whatever their number.
// For the zero project it is empty, which keeps every row.
func Condition(q Querier, p kernel.Project, column string) (cond string, args []any, err error) {
	if p.IsZero() {
		return "", nil, nil
	}
	folders, err := Folders(q, p)
	if err != nil {
		return "", nil, err
	}
	list, err := json.Marshal(folders)
	if err != nil {
		return "", nil, err
	}
	return column + " IN (SELECT value FROM json_each(?))", []any{string(list)}, nil
}
