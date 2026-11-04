package users

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MaxLinkSources bounds how many sources one link request may carry. A wallet
// is one phrase plus a handful of account keys, so 32 covers any real client in
// one call, and a client with more links them in several calls under the same
// signer. The cap exists because every row this endpoint writes is permanent
// and any self-minted key can write some: it bounds how far one request
// amplifies into storage and into ed25519 verifies.
const MaxLinkSources = 32

// Per-statement timeouts, applied with SET LOCAL so they end with the
// transaction. The advisory lock below blocks indefinitely by default, and a
// blocked link transaction pins a pool connection that every DB-backed route
// shares. lockTimeout bounds each lock wait and statementTimeout bounds each
// statement; that is all they bound. They start only once a pool connection
// has been acquired, and a transaction of several statements can outlive
// either, so they do not cap the transaction as a whole. That cap is the
// caller's context deadline (handlers.LinkRequestTimeout), which is what
// cancels both the wait for a connection and the transaction; the HTTP
// server's WriteTimeout does not cancel a handler. When either timeout here
// fires, Postgres cancels the statement and the error reaches Link as a
// *pgconn.PgError, not as a context error; Link maps both codes to ErrTimeout
// so the handler can answer with a 504 instead of a 500. Values are
// milliseconds; SET cannot take
// bind parameters, so they are formatted into the statement from these
// constants and never from input.
const (
	linkLockTimeoutMs      = 3000
	linkStatementTimeoutMs = 5000
)

// ErrTimeout means one of the transaction's own SET LOCAL timeouts fired: a
// lock wait ran past lockTimeout or a statement ran past statementTimeout. A
// lock wait is not only the signer lock: the INSERT also waits on a
// conflicting row that another transaction, possibly for a different signer,
// is still inserting. Either way it is a slow or contended database rather
// than a bug in the request. The *pgconn.PgError stays in the chain, so the
// log line still shows which SQLSTATE fired.
var ErrTimeout = errors.New("link statement timed out")

// SQLSTATE codes Postgres uses for the two timeouts above.
const (
	pgLockNotAvailable = "55P03" // lock_timeout: "canceling statement due to lock timeout"
	pgQueryCanceled    = "57014" // statement_timeout: "canceling statement due to statement timeout"
)

// classifyTimeout wraps a store error that was caused by one of the
// transaction's SET LOCAL timeouts in ErrTimeout, and returns every other
// error unchanged. The original error stays in the chain for the log line.
func classifyTimeout(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case pgLockNotAvailable, pgQueryCanceled:
		return fmt.Errorf("%w: %w", ErrTimeout, err)
	default:
		return err
	}
}

// Source is one entry of a link request after its consent has been verified:
// the canonical hex source id and the kind read from the signed bytes.
type Source struct {
	ID   string
	Kind string
}

// LinkedSource is one row of the caller's cluster as returned by Link.
type LinkedSource struct {
	ID        string
	Kind      string
	CreatedAt time.Time
}

// LinkResult is the outcome of one link call.
type LinkResult struct {
	// UserID is the caller's internal users.id (the foreign key for user-scoped
	// tables). It is not exposed to clients; CanonicalSourceID is.
	UserID uuid.UUID
	// CanonicalSourceID is the user id as exposed: the source that created the
	// caller's cluster. It never changes once set.
	CanonicalSourceID string
	// Sources is every source now in the caller's cluster, including rows that
	// predate this call. Never nil.
	Sources []LinkedSource
	// Conflicts is every submitted source id that belongs to ANOTHER user. Those
	// rows are left where they are: a claimed source is never moved. Never nil.
	Conflicts []string
	// Created reports that this call inserted a users row: the signer rooted a
	// new cluster. False when the signer already belonged to a user, and when
	// the signer lost the race for its own row and joined the cluster that
	// claimed it.
	Created bool
	// Written is the number of user_sources rows this call inserted.
	Written int
}

// TxBeginner is the subset of pgxpool.Pool the Linker needs: it owns the
// transaction that scopes the advisory lock, so it must begin one itself.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Linker performs the only write to the identity tables: resolving a verified
// link request into users and user_sources rows. See Link.
type Linker struct {
	db TxBeginner
}

// NewLinker returns a Linker that transacts on db.
func NewLinker(db TxBeginner) *Linker {
	return &Linker{db: db}
}

// Link resolves a link request whose consents the caller has ALREADY verified:
// every entry of sources carries a consent signed by its own key and bound to
// signerID, and signerID is itself one of sources. Link trusts its arguments on
// that basis and does no checks of its own: the handler applies every
// structural rule (bounds, canonical ids, no repeated id, signer present)
// before any signature work. The schema's CHECK constraints on source ids and
// kinds are the backstop, so a misuse fails its INSERT instead of writing a
// permanent row that could never resolve.
//
// Resolution runs inside one transaction with lock and statement timeouts:
//
//  1. Take ONE pg_advisory_xact_lock, on hashtext(signerID). Two callers
//     signing as the same source serialize here, which is what makes "exactly
//     one users row" hold when both are the first to claim it. No lock is taken
//     per source: who wins a contested source is decided by the user_sources
//     primary key in step 3, so per-source locks would add lock-table pressure
//     (up to MaxLinkSources held entries per transaction, against a shared
//     table every transaction in the database draws from) without adding a
//     guarantee.
//  2. Resolve the signer with ResolveUser on the transaction. If it has a row,
//     the caller is that user. If not, the signer roots a new cluster: under a
//     savepoint, insert a users row with canonical_source_id = signerID and go
//     to step 3 with the signer among the rows to write. If step 3 reports
//     that the signer's row was NOT written, another caller linked the signer
//     as one of its sources in the window since ResolveUser; roll back to the
//     savepoint, which discards the never-committed users row, and re-resolve:
//     the signer is now a member of that caller's cluster, exactly as if that
//     request had arrived first, which it did.
//  3. Write every source that has no row with one multi-row
//     INSERT ... ON CONFLICT (source_id) DO NOTHING RETURNING source_id, in
//     sorted id order. Sorting is what keeps two concurrent inserts with
//     overlapping ids from deadlocking each other. A conflicting row that
//     another transaction is still inserting makes this statement wait for it,
//     so an id that comes back unwritten has a committed row.
//  4. Re-read the unwritten ids. A row under the caller's user needs nothing; a
//     row under another user goes in Conflicts and is left untouched.
//
// Invariants this preserves: a claimed source is never moved, two populated
// clusters never combine, no row is ever retired, no users row is ever deleted,
// and canonical_source_id is never updated.
//
// Errors: ErrTimeout when the transaction's own lock or statement timeout
// fires (see classifyTimeout); the request context's error when it
// ends first; anything else, including a CHECK violation from malformed
// arguments, is a database failure.
func (l *Linker) Link(ctx context.Context, signerID string, sources []Source) (*LinkResult, error) {
	res, err := l.link(ctx, signerID, sources)
	if err != nil {
		return nil, classifyTimeout(err)
	}
	return res, nil
}

func (l *Linker) link(ctx context.Context, signerID string, sources []Source) (*LinkResult, error) {
	tx, err := l.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning link transaction: %w", err)
	}
	// Rollback after a successful Commit is a no-op (pgx returns ErrTxClosed,
	// which is ignored here), so this is safe on every path.
	defer tx.Rollback(ctx) //nolint:errcheck // rollback on the error path; no-op after commit

	// No bind parameters, so pgx uses the simple protocol and both statements go
	// in one round trip.
	if _, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL lock_timeout = %d; SET LOCAL statement_timeout = %d",
		linkLockTimeoutMs, linkStatementTimeoutMs)); err != nil {
		return nil, fmt.Errorf("setting link transaction timeouts: %w", err)
	}

	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, signerID); err != nil {
		return nil, fmt.Errorf("locking signer %s: %w", signerID, err)
	}

	// others is every source but the signer, in sorted id order; the signer is
	// prepended when it needs a row so the whole batch stays sorted (signerID
	// is canonical hex like the rest, so it sorts with them).
	others := make([]Source, 0, len(sources))
	for _, s := range sources {
		if s.ID != signerID {
			others = append(others, s)
		}
	}
	sortSources(others)

	userID, canonical, err := NewStore(tx).ResolveUser(ctx, signerID)
	var (
		written map[string]struct{}
		created bool
	)
	switch {
	case err == nil:
		// The caller is an existing user.
		written, err = insertSources(ctx, tx, userID, others)
		if err != nil {
			return nil, err
		}
	case errors.Is(err, ErrSourceNotFound):
		userID, canonical, written, created, err = createCluster(ctx, tx, signerID, kindOf(sources, signerID), others)
		if err != nil {
			return nil, err
		}
	default:
		return nil, err
	}

	// Classify what the INSERT skipped.
	conflicts := []string{}
	var unwritten []string
	for _, s := range others {
		if _, ok := written[s.ID]; !ok {
			unwritten = append(unwritten, s.ID)
		}
	}
	if len(unwritten) > 0 {
		var owners map[string]uuid.UUID
		owners, err = selectOwners(ctx, tx, unwritten)
		if err != nil {
			return nil, err
		}
		for _, id := range unwritten {
			owner, ok := owners[id]
			if !ok {
				// ON CONFLICT DO NOTHING skipped it, so a row existed and rows are
				// never deleted; not finding it now is an invariant violation.
				return nil, fmt.Errorf("source %s was skipped by insert but has no row", id)
			}
			if owner != userID {
				conflicts = append(conflicts, id)
			}
		}
	}

	cluster, err := selectCluster(ctx, tx, userID)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing link transaction: %w", err)
	}

	return &LinkResult{
		UserID:            userID,
		CanonicalSourceID: canonical,
		Sources:           cluster,
		Conflicts:         conflicts,
		Created:           created,
		Written:           len(written),
	}, nil
}

func kindOf(sources []Source, id string) string {
	for _, s := range sources {
		if s.ID == id {
			return s.Kind
		}
	}
	return ""
}

func sortSources(sources []Source) {
	slices.SortFunc(sources, func(a, b Source) int { return strings.Compare(a.ID, b.ID) })
}
}

// createCluster handles a signer with no user_sources row: it creates the users
// row and writes the signer's row together with others in one sorted INSERT,
// all under a savepoint. If the signer's own row comes back unwritten, another
// caller claimed the signer meanwhile; the savepoint is rolled back so the
// provisional users row never commits, and the signer is re-resolved as a
// member of that caller's cluster. created reports whether the users row was
// kept.
func createCluster(ctx context.Context, tx pgx.Tx, signerID, signerKind string, others []Source) (userID uuid.UUID, canonical string, written map[string]struct{}, created bool, err error) {
	sp, err := tx.Begin(ctx) // a nested Begin is a SAVEPOINT in pgx
	if err != nil {
		return uuid.Nil, "", nil, false, fmt.Errorf("creating savepoint: %w", err)
	}
	defer sp.Rollback(ctx) //nolint:errcheck // rollback on the error path; no-op after commit

	userID, err = createUser(ctx, sp, signerID)
	if err != nil {
		return uuid.Nil, "", nil, false, err
	}
	rows := make([]Source, 0, len(others)+1)
	rows = append(rows, Source{ID: signerID, Kind: signerKind})
	rows = append(rows, others...)
	sortSources(rows)
	written, err = insertSources(ctx, sp, userID, rows)
	if err != nil {
		return uuid.Nil, "", nil, false, err
	}
	if _, ok := written[signerID]; ok {
		if err = sp.Commit(ctx); err != nil {
			return uuid.Nil, "", nil, false, fmt.Errorf("releasing savepoint: %w", err)
		}
		return userID, signerID, written, true, nil
	}

	// Lost the race for the signer's own row. Discard everything done under the
	// savepoint and join the cluster that claimed the signer.
	if err = sp.Rollback(ctx); err != nil {
		return uuid.Nil, "", nil, false, fmt.Errorf("rolling back savepoint: %w", err)
	}
	userID, canonical, err = NewStore(tx).ResolveUser(ctx, signerID)
	if err != nil {
		return uuid.Nil, "", nil, false, fmt.Errorf("re-resolving signer %s after losing its insert: %w", signerID, err)
	}
	written, err = insertSources(ctx, tx, userID, others)
	if err != nil {
		return uuid.Nil, "", nil, false, err
	}
	return userID, canonical, written, false, nil
}

// usersCanonicalSourceIDKey is the UNIQUE constraint on users.canonical_source_id.
const usersCanonicalSourceIDKey = "users_canonical_source_id_key"

// createUser inserts the users row for a signer that has no user_sources row
// and returns its id. The advisory lock on the signer means no concurrent link
// can insert the same canonical id, so a unique violation here means a users
// row exists without its source row. No code path produces that (rows are
// never deleted, and a lost race rolls the row back with the savepoint), so it
// is a hand-made inconsistency: the error names the row to repair rather than
// working around it.
func createUser(ctx context.Context, tx pgx.Tx, signerID string) (uuid.UUID, error) {
	var userID uuid.UUID
	err := tx.QueryRow(ctx,
		`INSERT INTO users (canonical_source_id) VALUES ($1) RETURNING id`, signerID).Scan(&userID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == usersCanonicalSourceIDKey {
			return uuid.Nil, fmt.Errorf("users row for canonical source %s exists without its source row: %w", signerID, err)
		}
		return uuid.Nil, fmt.Errorf("creating user for source %s: %w", signerID, err)
	}
	return userID, nil
}

// insertSources writes rows under userID in one statement, skipping any id
// that already has a row, and returns the set of ids actually written. rows
// must be sorted by id (see Link). A nil or empty slice is a no-op.
func insertSources(ctx context.Context, tx pgx.Tx, userID uuid.UUID, rows []Source) (map[string]struct{}, error) {
	written := make(map[string]struct{}, len(rows))
	if len(rows) == 0 {
		return written, nil
	}
	ids := make([]string, len(rows))
	kinds := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
		kinds[i] = r.Kind
	}
	// WITH ORDINALITY pins the insert order to the sorted input: a bare unnest
	// in a SELECT carries no ordering guarantee once the planner is involved.
	res, err := tx.Query(ctx,
		`INSERT INTO user_sources (source_id, user_id, kind)
		 SELECT s.id, $2, s.kind
		 FROM unnest($1::text[], $3::text[]) WITH ORDINALITY AS s(id, kind, n)
		 ORDER BY s.n
		 ON CONFLICT (source_id) DO NOTHING
		 RETURNING source_id`,
		ids, userID, kinds)
	if err != nil {
		return nil, fmt.Errorf("inserting %d sources: %w", len(rows), err)
	}
	insertedIDs, err := pgx.CollectRows(res, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("collecting inserted sources: %w", err)
	}
	for _, id := range insertedIDs {
		written[id] = struct{}{}
	}
	return written, nil
}

// selectOwners returns user_id by source_id for the given ids.
func selectOwners(ctx context.Context, tx pgx.Tx, ids []string) (map[string]uuid.UUID, error) {
	rows, err := tx.Query(ctx,
		`SELECT source_id, user_id FROM user_sources WHERE source_id = ANY($1::text[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("reading owners of submitted sources: %w", err)
	}
	defer rows.Close()
	out := make(map[string]uuid.UUID, len(ids))
	for rows.Next() {
		var id string
		var owner uuid.UUID
		if err = rows.Scan(&id, &owner); err != nil {
			return nil, fmt.Errorf("scanning source owner: %w", err)
		}
		out[id] = owner
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("reading owners of submitted sources: %w", err)
	}
	return out, nil
}

func selectCluster(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]LinkedSource, error) {
	rows, err := tx.Query(ctx,
		`SELECT source_id, kind, created_at FROM user_sources WHERE user_id = $1 ORDER BY created_at, source_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("reading cluster: %w", err)
	}
	cluster, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (LinkedSource, error) {
		var s LinkedSource
		scanErr := row.Scan(&s.ID, &s.Kind, &s.CreatedAt)
		return s, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("collecting cluster: %w", err)
	}
	if cluster == nil {
		cluster = []LinkedSource{}
	}
	return cluster, nil
}
