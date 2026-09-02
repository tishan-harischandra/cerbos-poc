package keycloakbulkload

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultBatchSize bounds how many users' rows BulkLoad holds in memory and
// commits per transaction. Small enough that a killed run loses at most one
// batch's progress, large enough that per-batch transaction overhead does
// not dominate PostgreSQL's own COPY throughput.
const DefaultBatchSize = 20_000

// UserRecord is one seeded identity, resolved down to exactly what
// BulkLoad's raw SQL needs: Keycloak role UUIDs rather than names, so
// resolving 70 names per user against roleIDByName happens once in the
// generator, not once per batch.
type UserRecord struct {
	ID         string // a UUID; the generator's caller controls determinism
	Username   string
	FirstName  string
	LastName   string
	Email      string
	TenantID   string
	HospitalID string
	RoleIDs    []string
	// HospitalGroupIDs are the Keycloak group ids (issue #87) backing this
	// user's organization memberships - OrganizationGroupIDs's values, one
	// per hospital the generator placed this user in. Resolved once by the
	// caller, the same way RoleIDs already is, so a batch never repeats an
	// alias-to-group-id lookup per row.
	HospitalGroupIDs []string
}

// LoadConfig is everything BulkLoad needs beyond the users themselves.
type LoadConfig struct {
	RealmID    string
	Credential SharedCredential
	// BatchSize overrides DefaultBatchSize; zero means use the default.
	BatchSize int
	// Now is the creation timestamp baked into every user_entity row. A
	// parameter, not time.Now(), so a load run and its measured duration
	// agree on when "now" was.
	Now time.Time
}

// LoadStats reports what one BulkLoad call wrote, for the duration and
// footprint measurements issue #24's acceptance criteria ask for.
type LoadStats struct {
	Users        int
	RoleMappings int
	Memberships  int
	Batches      int
	// SkippedBatches is how many batches BulkLoad found already written by
	// an earlier, interrupted run (issue #87's "seeding is idempotent and
	// resumable") and left untouched rather than re-inserting.
	SkippedBatches int
	Elapsed        time.Duration
}

// BulkLoad reads users from the channel until it closes, and writes
// user_entity, user_attribute, credential and user_role_mapping rows for
// each of them via PostgreSQL's COPY protocol, DefaultBatchSize users at a
// time. The channel shape lets the caller generate 600,000 users without
// ever holding all of them in memory at once.
//
// Every user in one batch is written inside one transaction: a run killed
// mid-batch loses at most that batch, never leaves a user with some rows
// written and others missing.
func BulkLoad(ctx context.Context, pool *pgxpool.Pool, cfg LoadConfig, users <-chan UserRecord) (LoadStats, error) {
	if cfg.RealmID == "" {
		return LoadStats{}, fmt.Errorf("keycloakbulkload: a realm id is required")
	}
	if cfg.Credential.SecretData == "" || cfg.Credential.CredentialData == "" {
		return LoadStats{}, fmt.Errorf("keycloakbulkload: a shared credential is required")
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}
	now := cfg.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	createdTimestamp := now.UnixMilli()

	stats := LoadStats{}
	started := time.Now()

	batch := make([]UserRecord, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		// Issue #87's "seeding is idempotent and resumable": every user
		// in one batch commits in a single transaction (see writeBatch),
		// so a batch this run finds already fully written is exactly what
		// an earlier, interrupted run left behind - never a partial one -
		// and is safe to skip rather than fail on a primary-key conflict.
		already, err := batchAlreadyLoaded(ctx, pool, batch)
		if err != nil {
			return err
		}
		if already {
			stats.SkippedBatches++
			batch = batch[:0]
			return nil
		}
		if err := writeBatch(ctx, pool, cfg.RealmID, cfg.Credential, createdTimestamp, batch); err != nil {
			return err
		}
		stats.Users += len(batch)
		for _, u := range batch {
			stats.RoleMappings += len(u.RoleIDs)
			stats.Memberships += len(u.HospitalGroupIDs)
		}
		stats.Batches++
		batch = batch[:0]
		return nil
	}

	for user := range users {
		batch = append(batch, user)
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return stats, err
			}
		}
	}
	if err := flush(); err != nil {
		return stats, err
	}

	stats.Elapsed = time.Since(started)
	return stats, nil
}

// batchAlreadyLoaded reports whether every user in batch already has a
// user_entity row - the whole-batch-or-nothing signature of a batch a prior
// run already committed (writeBatch's one transaction per batch), as
// opposed to a batch this run has never attempted or one a killed run left
// only partially written and therefore never actually committed.
func batchAlreadyLoaded(ctx context.Context, pool *pgxpool.Pool, batch []UserRecord) (bool, error) {
	ids := make([]string, len(batch))
	for i, u := range batch {
		ids[i] = u.ID
	}
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_entity WHERE id = ANY($1)`, ids,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("keycloakbulkload: checking whether a batch was already loaded: %w", err)
	}
	return count == len(batch), nil
}

func writeBatch(ctx context.Context, pool *pgxpool.Pool, realmID string, cred SharedCredential, createdTimestamp int64, batch []UserRecord) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("keycloakbulkload: beginning a batch transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after a successful commit is a no-op

	if _, err := tx.CopyFrom(ctx,
		pgx.Identifier{"user_entity"},
		[]string{"id", "email", "email_verified", "enabled", "realm_id", "username",
			"first_name", "last_name", "created_timestamp", "not_before"},
		&userEntityRows{realmID: realmID, created: createdTimestamp, batch: batch},
	); err != nil {
		return fmt.Errorf("keycloakbulkload: copying user_entity: %w", err)
	}

	if _, err := tx.CopyFrom(ctx,
		pgx.Identifier{"user_attribute"},
		[]string{"id", "name", "value", "user_id"},
		&userAttributeRows{batch: batch},
	); err != nil {
		return fmt.Errorf("keycloakbulkload: copying user_attribute: %w", err)
	}

	if _, err := tx.CopyFrom(ctx,
		pgx.Identifier{"credential"},
		[]string{"id", "type", "user_id", "created_date", "secret_data", "credential_data", "priority", "version"},
		&credentialRows{cred: cred, created: createdTimestamp, batch: batch},
	); err != nil {
		return fmt.Errorf("keycloakbulkload: copying credential: %w", err)
	}

	if _, err := tx.CopyFrom(ctx,
		pgx.Identifier{"user_role_mapping"},
		[]string{"role_id", "user_id"},
		&roleMappingRows{batch: batch},
	); err != nil {
		return fmt.Errorf("keycloakbulkload: copying user_role_mapping: %w", err)
	}

	if _, err := tx.CopyFrom(ctx,
		pgx.Identifier{"user_group_membership"},
		[]string{"group_id", "user_id", "membership_type"},
		&membershipRows{batch: batch},
	); err != nil {
		return fmt.Errorf("keycloakbulkload: copying user_group_membership: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("keycloakbulkload: committing a batch: %w", err)
	}
	return nil
}
