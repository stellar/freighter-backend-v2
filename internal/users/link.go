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
//  3. Resolve the signer. If it has a row, the caller is that user. If not,
//     insert a users row with canonical_source_id = signerID and the signer's
//     row under it: the signer is the root of a new cluster.
//  4. For every other source: no row, insert it under the caller's user; a row
//     under the caller's user, nothing; a row under another user, report it in
//     Conflicts and leave it untouched.
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
	var userID uuid.UUID
	var canonical string
	if row, ok := existing[signerID]; ok {
		userID = row.userID
		if err = tx.QueryRow(ctx, `SELECT canonical_source_id FROM users WHERE id = $1`, userID).Scan(&canonical); err != nil {
			return nil, fmt.Errorf("reading canonical source for user %s: %w", userID, err)
		}
	} else {
		signerKind := kindOf(sources, signerID)
		if err = tx.QueryRow(ctx,
			`INSERT INTO users (canonical_source_id) VALUES ($1) RETURNING id`, signerID).Scan(&userID); err != nil {
			return nil, fmt.Errorf("creating user for source %s: %w", signerID, err)
		}
		if _, err = tx.Exec(ctx,
			`INSERT INTO user_sources (source_id, user_id, kind) VALUES ($1, $2, $3)`, signerID, userID, signerKind); err != nil {
			return nil, fmt.Errorf("inserting signer source %s: %w", signerID, err)
		}
		canonical = signerID
	}

	conflicts := []string{}
	for _, s := range sources {
		if s.ID == signerID {
			continue
		}
		row, ok := existing[s.ID]
		switch {
		case !ok:
			if _, err = tx.Exec(ctx,
				`INSERT INTO user_sources (source_id, user_id, kind) VALUES ($1, $2, $3)`, s.ID, userID, s.Kind); err != nil {
				return nil, fmt.Errorf("inserting source %s: %w", s.ID, err)
			}
		case row.userID == userID:
			// Already in the caller's cluster: nothing to do. The stored kind is the
			// one proven when the row was first written; it is not rewritten.
		default:
			conflicts = append(conflicts, s.ID)
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
	}, nil
}

// Validation errors Link returns before touching the database. They are
// sentinels so the handler can map them to 400 without string matching; the
// handler performs the same checks first (on the raw request, before any
// signature work), so hitting one here means a programming error, but the
// store refuses to rely on that.
var (
	ErrTooManySources    = errors.New("too many sources")
	ErrRepeatedSource    = errors.New("repeated source id")
	ErrSignerNotInBody   = errors.New("signing source is not among the sources")
	ErrUnknownSourceKind = errors.New("unknown source kind")
)

func validateLinkRequest(signerID string, sources []Source) error {
	if len(sources) > MaxLinkSources {
		return fmt.Errorf("%w: %d > %d", ErrTooManySources, len(sources), MaxLinkSources)
	}
	seen := make(map[string]struct{}, len(sources))
	signerPresent := false
	for _, s := range sources {
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
	for _, k := range keys {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(k)); err != nil {
			return fmt.Errorf("locking source key %d: %w", k, err)
		}
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
