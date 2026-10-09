package users

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stellar/freighter-backend-v2/internal/auth"
)

// MaxLinkSources bounds how many sources one link request may carry. A wallet
// links one phrase plus its account keys, so 128 is far above any real client
// and small enough that the advisory-lock loop and the IN list stay cheap.
const MaxLinkSources = 128

// Per-transaction timeouts, applied with SET LOCAL so they end with the
// transaction. The advisory locks below block indefinitely by default, and a
// blocked link transaction pins a pool connection that every DB-backed route
// shares, so a wait that outlives the HTTP write timeout (10s) helps nobody.
// lockTimeout bounds each lock wait; statementTimeout bounds every statement.
// Both are below the server write timeout so the client sees a clean error
// rather than a dropped connection. Values are milliseconds; SET cannot take
// bind parameters, so they are formatted into the statement from these
// constants and never from input.
const (
	linkLockTimeoutMs      = 3000
	linkStatementTimeoutMs = 5000
)

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
}

// TxBeginner is the subset of pgxpool.Pool the Linker needs: it owns the
// transaction that scopes the advisory locks, so it must begin one itself.
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
// it needs for its own correctness (bounds, no repeated id, signer present) so
// a misuse cannot reach the database.
//
// Resolution runs inside one transaction:
//
//  1. Take pg_advisory_xact_lock on hashtext(id) for every submitted id, with
//     the actual lock keys deduplicated and sorted numerically. Ordering the
//     keys rather than the ids is what prevents a lock-order reversal when two
//     ids hash to the same key. Two concurrent calls that share any source
//     therefore serialize, which is what makes "exactly one users row" hold when
//     two callers sign as the same unclaimed source at once.
//  2. Read every submitted id's existing row.
//  3. Resolve the signer with ResolveUser on the transaction. If it has a row,
//     the caller is that user. If not, the signer is the root of a new cluster:
//     insert a users row with canonical_source_id = signerID (or adopt one that
//     already carries that canonical id but lost its source row, so a repaired
//     or partially restored table cannot lock a wallet out forever) and the
//     signer's row under it.
//  4. For every other source: no row, insert it under the caller's user; a row
//     under the caller's user, nothing; a row under another user, report it in
//     Conflicts and leave it untouched. New rows go in with one multi-row
//     INSERT.
//
// Invariants this preserves: a claimed source is never moved, two populated
// clusters never combine, no row is ever retired, no users row is ever deleted,
// and canonical_source_id is never updated.
func (l *Linker) Link(ctx context.Context, signerID string, sources []Source) (*LinkResult, error) {
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

	ids := make([]string, len(sources))
	for i, s := range sources {
		ids[i] = s.ID
	}

	if err = lockSources(ctx, tx, ids); err != nil {
		return nil, err
	}

	existing, err := selectSources(ctx, tx, ids)
	if err != nil {
		return nil, err
	}

	// Resolve the signer to a user, creating the cluster if the signer is new.
	// newRows collects every source this call writes; the signer is one of them
	// when it is new, so a single INSERT covers both cases.
	var newRows []Source
	userID, canonical, err := NewStore(tx).ResolveUser(ctx, signerID)
	switch {
	case err == nil:
		// The caller is an existing user.
	case errors.Is(err, ErrSourceNotFound):
		userID, err = createOrAdoptUser(ctx, tx, signerID)
		if err != nil {
			return nil, err
		}
		canonical = signerID
		newRows = append(newRows, Source{ID: signerID, Kind: kindOf(sources, signerID)})
	default:
		return nil, err
	}

	conflicts := []string{}
	for _, s := range sources {
		if s.ID == signerID {
			continue
		}
		row, ok := existing[s.ID]
		switch {
		case !ok:
			newRows = append(newRows, s)
		case row.userID == userID:
			// Already in the caller's cluster: nothing to do. The stored kind is the
			// one proven when the row was first written; it is not rewritten.
		default:
			conflicts = append(conflicts, s.ID)
		}
	}

	if err = insertSources(ctx, tx, userID, newRows); err != nil {
		return nil, err
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
	seen := make(map[string]struct{}, len(sources))
	signerPresent := false
	if !auth.IsCanonicalSourceID(signerID) {
		return fmt.Errorf("%w: signer %q", ErrNonCanonicalSourceID, signerID)
	}
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

// lockSources takes a transaction-scoped advisory lock per distinct hashtext of
// the submitted ids, in ascending key order. hashtext is int4; the lock takes a
// bigint, and the implicit widening is stable, so the same id always maps to the
// same key across callers.
func lockSources(ctx context.Context, tx pgx.Tx, ids []string) error {
	rows, err := tx.Query(ctx, `SELECT hashtext(s) FROM unnest($1::text[]) AS s`, ids)
	if err != nil {
		return fmt.Errorf("hashing source ids: %w", err)
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[int32])
	if err != nil {
		return fmt.Errorf("collecting lock keys: %w", err)
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	// One round trip for all the locks. A batch executes its queries in queue
	// order, which is what the sorted keys rely on; an ORDER BY inside a single
	// SELECT pg_advisory_xact_lock(...) FROM ... would not guarantee the order
	// the function is evaluated in.
	batch := &pgx.Batch{}
	for _, k := range keys {
		batch.Queue(`SELECT pg_advisory_xact_lock($1)`, int64(k))
	}
	if err = tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("locking source keys: %w", err)
	}
	return nil
}

// createOrAdoptUser returns the users.id for a signer that has no user_sources
// row. Normally that is a fresh INSERT. If a users row already carries this
// canonical id, the signer's source row was lost (a manual repair, a partial
// restore); adopting that row instead of tripping the UNIQUE constraint keeps
// the wallet linkable and keeps the exposed user id stable, which is exactly
// what canonical_source_id promises.
func createOrAdoptUser(ctx context.Context, tx pgx.Tx, signerID string) (uuid.UUID, error) {
	var userID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM users WHERE canonical_source_id = $1`, signerID).Scan(&userID)
	switch {
	case err == nil:
		return userID, nil
	case errors.Is(err, pgx.ErrNoRows):
		// Fall through to insert.
	default:
		return uuid.Nil, fmt.Errorf("looking up user by canonical source %s: %w", signerID, err)
	}
	if err = tx.QueryRow(ctx,
		`INSERT INTO users (canonical_source_id) VALUES ($1) RETURNING id`, signerID).Scan(&userID); err != nil {
		return uuid.Nil, fmt.Errorf("creating user for source %s: %w", signerID, err)
	}
	return userID, nil
}

// insertSources writes rows under userID in one statement. A nil or empty slice
// is a no-op.
func insertSources(ctx context.Context, tx pgx.Tx, userID uuid.UUID, rows []Source) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]string, len(rows))
	kinds := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
		kinds[i] = r.Kind
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_sources (source_id, user_id, kind)
		 SELECT s.id, $2, s.kind FROM unnest($1::text[], $3::text[]) AS s(id, kind)`,
		ids, userID, kinds); err != nil {
		return fmt.Errorf("inserting %d sources: %w", len(rows), err)
	}
	return nil
}

type sourceRow struct {
	userID uuid.UUID
	kind   string
}

func selectSources(ctx context.Context, tx pgx.Tx, ids []string) (map[string]sourceRow, error) {
	rows, err := tx.Query(ctx,
		`SELECT source_id, user_id, kind FROM user_sources WHERE source_id = ANY($1::text[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("reading submitted sources: %w", err)
	}
	defer rows.Close()
	out := make(map[string]sourceRow, len(ids))
	for rows.Next() {
		var id string
		var r sourceRow
		if err = rows.Scan(&id, &r.userID, &r.kind); err != nil {
			return nil, fmt.Errorf("scanning submitted source: %w", err)
		}
		out[id] = r
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("reading submitted sources: %w", err)
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
