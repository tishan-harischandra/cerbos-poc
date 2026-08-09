// Package databaselock is the leader election adapter backed by a lease row
// in the authorization database.
//
// It exists because PG_ADVISORY cannot run on Oracle, and an installation on
// the oracle profile still needs the policy controller and the outbox
// publisher to have exactly one active instance. It runs on either engine
// with no second piece of infrastructure to keep available.
//
// The lease is a genuine lease, so it admits the split-brain window the port
// documents: a leader that is paused past its expiry can still be inside
// onElected when a rival takes the row. What is ruled out is the sharper
// failure - two leaders because two clocks disagree. Every comparison and
// every expiry is evaluated by the database, from the database's own clock,
// so no application clock takes part in the decision at all.
//
// Nothing outside the provider factory may import this package.
package databaselock

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	_ "github.com/sijms/go-ora/v2"     // registers the pure-Go "oracle" driver
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/lease"
)

// Dialect names the SQL this adapter speaks.
type Dialect string

const (
	// DialectPostgres uses ON CONFLICT and now().
	DialectPostgres Dialect = "postgres"
	// DialectOracle uses MERGE and SYSTIMESTAMP.
	DialectOracle Dialect = "oracle"
)

// statementTimeout bounds one lease query. It is short on purpose: a query
// that outlives the lease it is renewing has already lost the election, and
// waiting longer only delays finding out.
const statementTimeout = 5 * time.Second

// Config describes the database the election runs in.
type Config struct {
	// DSN is the authorization database. The dialect is taken from its
	// scheme unless Dialect is set.
	DSN string
	// Dialect overrides the dialect inferred from the DSN.
	Dialect Dialect
	// Identity names this instance in the lease row.
	Identity string

	TTL           time.Duration
	RenewInterval time.Duration
	RetryInterval time.Duration

	// PauseRenewal reproduces a paused or killed leader for the contract
	// suite. Production leaves it nil.
	PauseRenewal <-chan struct{}
	// OnError, if set, receives backend failures.
	OnError func(error)
}

// Elector contends for one row of the leader_lock table.
type Elector struct {
	cfg      Config
	dialect  Dialect
	db       *sql.DB
	election leaderlock.Name
}

// New opens the database and returns the adapter.
func New(cfg Config) (*Elector, error) {
	if cfg.DSN == "" {
		return nil, errors.New("databaselock: a DSN is required")
	}
	if cfg.Identity == "" {
		return nil, errors.New("databaselock: an identity is required, or a lease names nobody")
	}
	if cfg.TTL <= 0 {
		return nil, errors.New("databaselock: a ttl is required: this lease expires on time, not on a session ending")
	}

	dialect := cfg.Dialect
	if dialect == "" {
		dialect = DialectFor(cfg.DSN)
	}
	driver, err := driverFor(dialect)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("databaselock: opening %s: %w", dialect, err)
	}
	// One contender needs one connection, and holding a whole pool open
	// for a row that is touched once every renewal interval is waste.
	db.SetMaxOpenConns(2)

	return &Elector{cfg: cfg, dialect: dialect, db: db}, nil
}

// DialectFor infers the dialect from a DSN's scheme. Inferring rather than
// asking keeps one more setting out of every manifest, and the two schemes
// are unambiguous.
func DialectFor(dsn string) Dialect {
	if strings.HasPrefix(strings.ToLower(dsn), "oracle:") {
		return DialectOracle
	}
	return DialectPostgres
}

func driverFor(dialect Dialect) (string, error) {
	switch dialect {
	case DialectPostgres:
		return "pgx", nil
	case DialectOracle:
		return "oracle", nil
	default:
		return "", fmt.Errorf("databaselock: %q is not a dialect this adapter speaks", dialect)
	}
}

// Close releases the database handle.
func (e *Elector) Close() error { return e.db.Close() }

// Run contends for the lease row the election name keys.
func (e *Elector) Run(ctx context.Context, election leaderlock.Name, onElected func(context.Context)) error {
	if err := election.Validate(); err != nil {
		return err
	}
	e.election = election

	return lease.Run(ctx, lease.Config{
		TTL:           e.cfg.TTL,
		RenewInterval: e.cfg.RenewInterval,
		RetryInterval: e.cfg.RetryInterval,
		PauseRenewal:  e.cfg.PauseRenewal,
		OnError:       e.cfg.OnError,
	}, e, onElected)
}

// Acquire claims the lease if it is free, expired, or already ours.
//
// The whole decision is one statement, so there is no window between reading
// the row and taking it. Two contenders racing here are serialised by the
// primary key: one updates the row, the other matches nothing.
func (e *Elector) Acquire(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()

	seconds := e.ttlSeconds()
	var (
		result sql.Result
		err    error
	)
	switch e.dialect {
	case DialectPostgres:
		result, err = e.db.ExecContext(ctx, `
INSERT INTO leader_lock (election_name, holder, expires_at, acquired_at)
VALUES ($1, $2, now() + make_interval(secs => $3), now())
ON CONFLICT (election_name) DO UPDATE
   SET holder      = EXCLUDED.holder,
       expires_at  = EXCLUDED.expires_at,
       acquired_at = CASE WHEN leader_lock.holder = EXCLUDED.holder
                          THEN leader_lock.acquired_at ELSE now() END
 WHERE leader_lock.expires_at <= now()
    OR leader_lock.holder = EXCLUDED.holder`,
			string(e.election), e.cfg.Identity, seconds)
	case DialectOracle:
		result, err = e.db.ExecContext(ctx, `
MERGE INTO leader_lock l
USING (SELECT :1 AS election_name, :2 AS holder FROM dual) s
   ON (l.election_name = s.election_name)
 WHEN MATCHED THEN
      UPDATE SET l.holder      = s.holder,
                 l.expires_at  = SYSTIMESTAMP + NUMTODSINTERVAL(:3, 'SECOND'),
                 l.acquired_at = CASE WHEN l.holder = s.holder
                                      THEN l.acquired_at ELSE SYSTIMESTAMP END
       WHERE l.expires_at <= SYSTIMESTAMP OR l.holder = s.holder
 WHEN NOT MATCHED THEN
      INSERT (election_name, holder, expires_at, acquired_at)
      VALUES (s.election_name, s.holder, SYSTIMESTAMP + NUMTODSINTERVAL(:4, 'SECOND'), SYSTIMESTAMP)`,
			string(e.election), e.cfg.Identity, seconds, seconds)
	default:
		return false, fmt.Errorf("databaselock: %q is not a dialect this adapter speaks", e.dialect)
	}
	if err != nil {
		// Two contenders inserting the same brand-new election at the
		// same instant is a race the primary key settles, not an
		// outage: one of them simply did not win.
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, fmt.Errorf("%w: databaselock: acquiring: %w", leaderlock.ErrBackendUnavailable, err)
	}
	return affectedOne(result)
}

// Renew extends a lease this instance still holds. Matching on the holder is
// what stops a deposed leader quietly extending the lease a rival now owns.
func (e *Elector) Renew(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()

	seconds := e.ttlSeconds()
	var (
		result sql.Result
		err    error
	)
	switch e.dialect {
	case DialectPostgres:
		result, err = e.db.ExecContext(ctx, `
UPDATE leader_lock
   SET expires_at = now() + make_interval(secs => $3)
 WHERE election_name = $1
   AND holder = $2
   AND expires_at > now()`,
			string(e.election), e.cfg.Identity, seconds)
	case DialectOracle:
		// The placeholders ascend in the order they appear, because
		// go-ora binds positionally and ignores the number: writing
		// the SET clause as :3 bound the election name to it and the
		// ttl to election_name, so every renewal matched no row and
		// the leader stood down at its first renewal - on Oracle
		// only. The same trap is recorded against the outbox in
		// oraclestore (issue #48).
		result, err = e.db.ExecContext(ctx, `
UPDATE leader_lock
   SET expires_at = SYSTIMESTAMP + NUMTODSINTERVAL(:1, 'SECOND')
 WHERE election_name = :2
   AND holder = :3
   AND expires_at > SYSTIMESTAMP`,
			seconds, string(e.election), e.cfg.Identity)
	default:
		return false, fmt.Errorf("databaselock: %q is not a dialect this adapter speaks", e.dialect)
	}
	if err != nil {
		return false, fmt.Errorf("%w: databaselock: renewing: %w", leaderlock.ErrBackendUnavailable, err)
	}
	return affectedOne(result)
}

// Release hands the election over at once rather than making the fleet wait
// out the ttl. Deleting only our own row means a slow shutdown cannot remove
// the lease a rival has already taken.
func (e *Elector) Release(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()

	query := `DELETE FROM leader_lock WHERE election_name = $1 AND holder = $2`
	if e.dialect == DialectOracle {
		query = `DELETE FROM leader_lock WHERE election_name = :1 AND holder = :2`
	}
	if _, err := e.db.ExecContext(ctx, query, string(e.election), e.cfg.Identity); err != nil {
		return fmt.Errorf("databaselock: releasing: %w", err)
	}
	return nil
}

// ttlSeconds is the lease length as the database's interval arithmetic wants
// it. Fractional seconds are kept so a test can use a short lease.
func (e *Elector) ttlSeconds() float64 { return e.cfg.TTL.Seconds() }

func affectedOne(result sql.Result) (bool, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("databaselock: reading the affected row count: %w", err)
	}
	return affected == 1, nil
}

// isDuplicateKey recognises the primary key collision two contenders can
// cause by inserting the same new election at once. The two engines report it
// with different codes and neither exposes a typed error worth importing a
// driver package for, so the codes are matched as text.
func isDuplicateKey(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "23505") || // PostgreSQL unique_violation
		strings.Contains(text, "ora-00001") // Oracle unique constraint violated
}
