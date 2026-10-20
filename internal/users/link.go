package users

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/stellar/freighter-backend-v2/internal/auth"
)

// MaxLinkSources bounds how many sources one link request may carry. A wallet
// is one phrase plus a handful of account keys, so 32 covers any real client in
// one call, and a client with more links them in several calls under the same
// signer. The cap exists because every row this endpoint writes is permanent
// and any self-minted key can write some: it bounds how far one request
// amplifies into storage and into ed25519 verifies. #165 specified 128; it was
// lowered for that reason, since nothing in a request justifies the larger
// number.
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
// *pgconn.PgError, not as a context error; Link maps the two codes to
// ErrLockTimeout and ErrStatementTimeout so the handler can answer with a
// retryable status instead of a 500. Values are milliseconds; SET cannot take
// bind parameters, so they are formatted into the statement from these
// constants and never from input.
const (
	linkLockTimeoutMs      = 3000
	linkStatementTimeoutMs = 5000
)

// Timeouts the transaction's own SET LOCAL settings raise. Both are transient:
// ErrLockTimeout means another link signed as the same source held the signer
// lock for longer than lockTimeout, and the caller should retry shortly;
// ErrStatementTimeout means one statement ran past statementTimeout, which is
// a slow or contended database rather than a bug in the request.
var (
	ErrLockTimeout      = errors.New("timed out waiting for the signer lock")
	ErrStatementTimeout = errors.New("statement timed out")
)

// SQLSTATE codes Postgres uses for the two timeouts above.
const (
	pgLockNotAvailable = "55P03" // lock_timeout: "canceling statement due to lock timeout"
	pgQueryCanceled    = "57014" // statement_timeout: "canceling statement due to statement timeout"
)

// classifyTimeout wraps a store error that was caused by one of the
// transaction's SET LOCAL timeouts in its sentinel, and returns every other
// error unchanged. The original error stays in the chain for the log line.
func classifyTimeout(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case pgLockNotAvailable:
		return fmt.Errorf("%w: %w", ErrLockTimeout, err)
	case pgQueryCanceled:
		return fmt.Errorf("%w: %w", ErrStatementTimeout, err)
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
	// new cluster. False when the signer already belonged to a user, when an
	// orphaned users row was adopted, and when the signer lost the race for its
	// own row and joined the cluster that claimed it.
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
// that basis and does no signature work. It enforces only the structural rules
// it needs for its own correctness (bounds, canonical ids, no repeated id,
// signer present) so a misuse cannot reach the database.
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
//     savepoint, insert a users row with canonical_source_id = signerID (or
//     adopt one that already carries that canonical id but lost its source row,
//     so a repaired or partially restored table cannot lock a wallet out) and
//     go to step 3 with the signer among the rows to write. If step 3 reports
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
// Errors: the validation sentinels below for a malformed request;
// ErrLockTimeout and ErrStatementTimeout when the transaction's own timeouts
// fire (see classifyTimeout); the request context's error when it ends first;
// anything else is a database failure.
func (l *Linker) Link(ctx context.Context, signerID string, sources []Source) (*LinkResult, error) {
	res, err := l.link(ctx, signerID, sources)
	if err != nil {
		return nil, classifyTimeout(err)
	}
	return res, nil
}

func (l *Linker) link(ctx context.Context, signerID string, sources []Source) (*LinkResult, error) {
	if err := validateLinkRequest(signerID, sources); err != nil {
		return nil, err
	}

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

	cluster, err := NewStore(tx).ListSources(ctx, userID)
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

// Validation errors Link returns before touching the database. They are
// sentinels so the handler can map them to 400 without string matching; the
// handler performs the same checks first (on the raw request, before any
// signature work), so hitting one here means a programming error, but the
// store refuses to rely on that.
var (
	ErrTooManySources       = errors.New("too many sources")
	ErrRepeatedSource       = errors.New("repeated source id")
	ErrSignerNotInBody      = errors.New("signing source is not among the sources")
	ErrUnknownSourceKind    = errors.New("unknown source kind")
	ErrNonCanonicalSourceID = errors.New("source id is not a 64-character lowercase hex public key")
)

func validateLinkRequest(signerID string, sources []Source) error {
	if len(sources) > MaxLinkSources {
		return fmt.Errorf("%w: %d > %d", ErrTooManySources, len(sources), MaxLinkSources)
	}
	if !auth.IsCanonicalSourceID(signerID) {
		return fmt.Errorf("%w: signer %q", ErrNonCanonicalSourceID, signerID)
	}
	seen := make(map[string]struct{}, len(sources))
	signerPresent := false
	for _, s := range sources {
		// The JWT parser lowercases sub, and ResolveUser is keyed by that form, so
		// a row written under any other spelling would never resolve again.
		if !auth.IsCanonicalSourceID(s.ID) {
			return fmt.Errorf("%w: %q", ErrNonCanonicalSourceID, s.ID)
		}
		if _, dup := seen[s.ID]; dup {
			return fmt.Errorf("%w: %s", ErrRepeatedSource, s.ID)
		}
		seen[s.ID] = struct{}{}
		if !auth.IsValidSourceKind(s.Kind) {
			return fmt.Errorf("%w: %q", ErrUnknownSourceKind, s.Kind)
		}
		if s.ID == signerID {
			signerPresent = true
		}
	}
	if !signerPresent {
		return ErrSignerNotInBody
	}
	return nil
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
	slices.SortFunc(sources, func(a, b Source) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
}

// createCluster handles a signer with no user_sources row: it creates (or
// adopts) the users row and writes the signer's row together with others in
// one sorted INSERT, all under a savepoint. If the signer's own row comes back
// unwritten, another caller claimed the signer meanwhile; the savepoint is
// rolled back so the provisional users row never commits, and the signer is
// re-resolved as a member of that caller's cluster. created reports whether a
// users row was actually inserted and kept.
func createCluster(ctx context.Context, tx pgx.Tx, signerID, signerKind string, others []Source) (userID uuid.UUID, canonical string, written map[string]struct{}, created bool, err error) {
	sp, err := tx.Begin(ctx) // a nested Begin is a SAVEPOINT in pgx
	if err != nil {
		return uuid.Nil, "", nil, false, fmt.Errorf("creating savepoint: %w", err)
	}
	defer sp.Rollback(ctx) //nolint:errcheck // rollback on the error path; no-op after commit

	userID, created, err = createOrAdoptUser(ctx, sp, signerID)
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
		return userID, signerID, written, created, nil
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

// createOrAdoptUser returns the users.id for a signer that has no user_sources
// row. Normally that is a fresh INSERT. If a users row already carries this
// canonical id, the signer's source row was lost (a manual repair, a partial
// restore); adopting that row instead of tripping the UNIQUE constraint keeps
// the wallet linkable and keeps the exposed user id stable, which is exactly
// what canonical_source_id promises. created is true only for a fresh INSERT.
func createOrAdoptUser(ctx context.Context, tx pgx.Tx, signerID string) (userID uuid.UUID, created bool, err error) {
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE canonical_source_id = $1`, signerID).Scan(&userID)
	switch {
	case err == nil:
		return userID, false, nil
	case errors.Is(err, pgx.ErrNoRows):
		// Fall through to insert.
	default:
		return uuid.Nil, false, fmt.Errorf("looking up user by canonical source %s: %w", signerID, err)
	}
	if err = tx.QueryRow(ctx,
		`INSERT INTO users (canonical_source_id) VALUES ($1) RETURNING id`, signerID).Scan(&userID); err != nil {
		return uuid.Nil, false, fmt.Errorf("creating user for source %s: %w", signerID, err)
	}
	return userID, true, nil
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
