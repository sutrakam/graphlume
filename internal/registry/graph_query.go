package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GraphQueryResult holds the tabular output of a read-only declarative query.
type GraphQueryResult struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	TotalRows int      `json:"total_rows"`
	Truncated bool     `json:"truncated"`
}

// QueryGraph executes a guarded read-only SELECT query against the SQLite index database.
func (r *Registry) QueryGraph(ctx context.Context, query string, maxRows int) (*GraphQueryResult, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("registry: database is not open")
	}

	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("query: empty query string")
	}

	// Strictly enforce read-only SELECT / WITH queries
	upper := strings.ToUpper(q)
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return nil, errors.New("query: only read-only SELECT and WITH queries are permitted")
	}

	// Reject semicolon chaining of multiple statements
	trimmedSemi := strings.TrimRight(q, "; \t\r\n")
	if strings.Contains(trimmedSemi, ";") {
		return nil, errors.New("query: multi-statement queries containing ';' are not permitted")
	}

	// Blacklist mutating and admin statements
	forbidden := []string{
		"INSERT ", "UPDATE ", "DELETE ", "DROP ", "ALTER ", "ATTACH ", "DETACH ",
		"CREATE ", "REPLACE ", "TRUNCATE ", "PRAGMA ", "VACUUM ", "REINDEX ",
	}
	for _, kw := range forbidden {
		if strings.Contains(upper, kw) {
			return nil, fmt.Errorf("query: forbidden keyword %q in read-only query", strings.TrimSpace(kw))
		}
	}

	if maxRows <= 0 {
		maxRows = 50
	}
	if maxRows > 200 {
		maxRows = 200
	}

	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	rows, err := r.db.QueryContext(queryCtx, q)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("query columns: %w", err)
	}

	res := &GraphQueryResult{
		Columns: cols,
		Rows:    make([][]any, 0, maxRows),
	}

	rowCount := 0
	for rows.Next() {
		if rowCount >= maxRows {
			res.Truncated = true
			break
		}
		rawValues := make([]any, len(cols))
		scanArgs := make([]any, len(cols))
		for i := range rawValues {
			scanArgs[i] = &rawValues[i]
		}
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		cleanRow := make([]any, len(cols))
		for i, v := range rawValues {
			switch val := v.(type) {
			case []byte:
				cleanRow[i] = string(val)
			case nil:
				cleanRow[i] = nil
			case sql.NullString:
				if val.Valid {
					cleanRow[i] = val.String
				} else {
					cleanRow[i] = nil
				}
			default:
				cleanRow[i] = val
			}
		}
		res.Rows = append(res.Rows, cleanRow)
		rowCount++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	res.TotalRows = len(res.Rows)
	return res, nil
}
