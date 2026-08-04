package oraclestore

import (
	"context"
	"database/sql"
	"fmt"
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

func (i inspector) Indexes(ctx context.Context, table string) (map[string][]string, error) {
	const query = `
		SELECT i.index_name, ic.column_name
		FROM user_indexes i
		JOIN user_ind_columns ic ON ic.index_name = i.index_name
		WHERE i.table_name = UPPER(:1)
		ORDER BY i.index_name, ic.column_position`

	return i.collect(ctx, query, table)
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
