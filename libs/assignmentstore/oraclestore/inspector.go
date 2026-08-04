package oraclestore

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// inspector reads Oracle catalog metadata.
//
// Oracle folds unquoted identifiers to upper case, so everything is lowered on
// the way out: the contract suite compares against the lower-case names the
// changelog declares and must not have to know which engine folded them.
type inspector struct {
	db *sql.DB
}

func (i inspector) Tables(ctx context.Context) ([]string, error) {
	const query = `SELECT table_name FROM user_tables`

	rows, err := i.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("oraclestore: listing tables: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("oraclestore: scanning a table name: %w", err)
		}
		tables = append(tables, strings.ToLower(name))
	}
	return tables, rows.Err()
}

func (i inspector) UniqueKeys(ctx context.Context, table string) (map[string][]string, error) {
	const query = `
		SELECT c.constraint_name, cc.column_name
		FROM user_constraints c
		JOIN user_cons_columns cc ON cc.constraint_name = c.constraint_name
		WHERE c.table_name = UPPER(:1) AND c.constraint_type IN ('P', 'U')
		ORDER BY c.constraint_name, cc.position`

	return i.collect(ctx, query, table)
}

// Indexes reports index columns, resolving Oracle's hidden system columns back to
// the columns the schema actually declared.
//
// Indexing a TIMESTAMP WITH TIME ZONE column makes Oracle build a function-based
// index over an invisible virtual column, and user_ind_columns then reports that
// column as SYS_NC00012$ rather than as valid_from. The real name is recoverable
// from user_ind_expressions, and recovering it here is the adapter's job: a caller
// asking which columns are indexed should not have to know that one engine
// answers with a generated identifier.
func (i inspector) Indexes(ctx context.Context, table string) (map[string][]string, error) {
	const query = `
		SELECT i.index_name, ic.column_name, e.column_expression
		FROM user_indexes i
		JOIN user_ind_columns ic ON ic.index_name = i.index_name
		LEFT JOIN user_ind_expressions e
			ON e.index_name = ic.index_name
			AND e.column_position = ic.column_position
		WHERE i.table_name = UPPER(:1)
		ORDER BY i.index_name, ic.column_position`

	rows, err := i.db.QueryContext(ctx, query, table)
	if err != nil {
		return nil, fmt.Errorf("oraclestore: reading indexes for %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	grouped := make(map[string][]string)
	for rows.Next() {
		var name, column string
		var expression sql.NullString
		if err := rows.Scan(&name, &column, &expression); err != nil {
			return nil, fmt.Errorf("oraclestore: scanning indexes for %s: %w", table, err)
		}
		if systemColumn.MatchString(column) && expression.Valid {
			if resolved := columnInExpression(expression.String); resolved != "" {
				column = resolved
			}
		}
		lowered := strings.ToLower(name)
		grouped[lowered] = append(grouped[lowered], strings.ToLower(column))
	}
	return grouped, rows.Err()
}

// systemColumn matches the names Oracle generates for the invisible virtual
// columns behind a function-based index, for example SYS_NC00012$.
var systemColumn = regexp.MustCompile(`^SYS_NC\d+\$$`)

// quotedIdentifier picks the column name out of an index expression such as
// SYS_EXTRACT_UTC("VALID_FROM").
var quotedIdentifier = regexp.MustCompile(`"([A-Za-z0-9_$#]+)"`)

func columnInExpression(expression string) string {
	// The last quoted identifier is the innermost operand, which is the column
	// the expression is computed from.
	matches := quotedIdentifier.FindAllStringSubmatch(expression, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1][1]
}

func (i inspector) collect(ctx context.Context, query, table string) (map[string][]string, error) {
	rows, err := i.db.QueryContext(ctx, query, table)
	if err != nil {
		return nil, fmt.Errorf("oraclestore: reading metadata for %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	grouped := make(map[string][]string)
	for rows.Next() {
		var name, column string
		if err := rows.Scan(&name, &column); err != nil {
			return nil, fmt.Errorf("oraclestore: scanning metadata for %s: %w", table, err)
		}
		lowered := strings.ToLower(name)
		grouped[lowered] = append(grouped[lowered], strings.ToLower(column))
	}
	return grouped, rows.Err()
}
