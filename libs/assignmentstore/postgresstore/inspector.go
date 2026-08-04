package postgresstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// inspector reads PostgreSQL catalog metadata so the contract suite can assert
// the §8.2 constraints without knowing which engine it is talking to.
type inspector struct {
	pool *pgxpool.Pool
}

func (i inspector) Tables(ctx context.Context) ([]string, error) {
	const query = `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`

	rows, err := i.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: listing tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("postgresstore: scanning a table name: %w", err)
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

func (i inspector) UniqueKeys(ctx context.Context, table string) (map[string][]string, error) {
	// Both primary keys and unique constraints are reported: either one enforces
	// the uniqueness §8.2 asks for, and which one a schema chose is not something
	// the contract should care about.
	const query = `
		SELECT c.conname, a.attname
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN LATERAL unnest(c.conkey) WITH ORDINALITY AS k(attnum, ord) ON TRUE
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		WHERE t.relname = $1 AND c.contype IN ('p', 'u')
		ORDER BY c.conname, k.ord`

	return i.collect(ctx, query, table)
}

func (i inspector) Indexes(ctx context.Context, table string) (map[string][]string, error) {
	const query = `
		SELECT i.relname, a.attname
		FROM pg_index x
		JOIN pg_class t ON t.oid = x.indrelid
		JOIN pg_class i ON i.oid = x.indexrelid
		JOIN LATERAL unnest(x.indkey) WITH ORDINALITY AS k(attnum, ord) ON TRUE
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		WHERE t.relname = $1
		ORDER BY i.relname, k.ord`

	return i.collect(ctx, query, table)
}

func (i inspector) collect(ctx context.Context, query, table string) (map[string][]string, error) {
	rows, err := i.pool.Query(ctx, query, table)
	if err != nil {
		return nil, fmt.Errorf("postgresstore: reading metadata for %s: %w", table, err)
	}
	defer rows.Close()

	grouped := make(map[string][]string)
	for rows.Next() {
		var name, column string
		if err := rows.Scan(&name, &column); err != nil {
			return nil, fmt.Errorf("postgresstore: scanning metadata for %s: %w", table, err)
		}
		grouped[name] = append(grouped[name], column)
	}
	return grouped, rows.Err()
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
