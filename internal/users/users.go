// Package users owns data access for the identity tables of the Unified User
// Model v2: `users` and `user_sources` (migration
// internal/db/migrations/2026-10-07.0-users_and_user_sources.sql).
//
// A user is a cluster of SOURCES: every seed phrase and account secret key the
// user has proven possession of, one `user_sources` row each. `users.id` is the
// internal foreign key that every user-scoped table references, and
// `users.canonical_source_id` (the source that created the user) is THE user id
// as exposed to clients. It is set once on insert and never changes.
//
// # Access rule for user-scoped handlers
//
// The auth middleware verifies the JWT signature and attaches only the SOURCE id
// (the `sub`) to the request context. It never touches the database. Mapping a
// source to its user is one primary-key lookup that only the handlers that need a
// user pay for. Every handler that reads or writes user-scoped data must:
//
//  1. Read the source id from the context with auth.SourceIDFromContext. If none
//     is set the request is anonymous (permissive mode); a user-scoped route must
//     be wrapped with auth.Required so this cannot happen.
//  2. Call ResolveUser with it. ErrSourceNotFound means the source has never been
//     linked and maps to 404: there is no user to scope by yet. Any other error
//     is operational (500).
//  3. Scope EVERY query by the returned user id (`WHERE user_id = $1`). Never key
//     storage by the source id, and never accept a user id or source id from the
//     request body, path, or query: the only trusted identity is the one the
//     signature proved.
//
// There is no cache in front of ResolveUser, on purpose. The mapping is immutable
// today (no code path moves a source between users, retires a source, or deletes
// a user), so a cache would need no invalidation, but it would also save Postgres
// CPU rather than latency: Redis is a round trip to another service, the same as
// the primary-key lookup it would replace. Add one only if the lookup shows up in
// profiles, and only after whatever makes the mapping mutable also defines how the
// cache is invalidated.
package users

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrSourceNotFound is returned by ResolveUser when the source id has no
// `user_sources` row: the caller has proven possession of a key, but that key
// has never been linked to a user. Handlers map it to 404. It is a sentinel, not
// a wrapped database error, so callers test it with errors.Is.
var ErrSourceNotFound = errors.New("source id is not linked to any user")

// Querier is the subset of pgx that ResolveUser needs. Both *pgxpool.Pool and
// pgx.Tx satisfy it, so a handler that resolves the caller inside the same
// transaction as its writes (the link handler) can pass the transaction and
// read its own uncommitted rows.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store reads the identity tables.
type Store struct {
	db Querier
}

// NewStore returns a Store that queries through db.
func NewStore(db Querier) *Store {
	return &Store{db: db}
}

// resolveUserSQL is one primary-key lookup on user_sources followed by a
// primary-key join to users. There is no index to add and nothing to cache.
const resolveUserSQL = `
SELECT u.id, u.canonical_source_id
FROM user_sources AS s
JOIN users AS u ON u.id = s.user_id
WHERE s.source_id = $1`

// ResolveUser maps a source id (the JWT `sub`, as set in the request context by
// the auth middleware) to the user that owns it. It returns the user's internal
// id, which every user-scoped query must filter by, and the user's canonical
// source id, which is the user id as exposed to clients.
//
// A source with no row returns ErrSourceNotFound. Any other error is wrapped
// and should be treated as operational.
func (s *Store) ResolveUser(ctx context.Context, sourceID string) (userID uuid.UUID, canonicalSourceID string, err error) {
	err = s.db.QueryRow(ctx, resolveUserSQL, sourceID).Scan(&userID, &canonicalSourceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, "", ErrSourceNotFound
		}
		return uuid.Nil, "", fmt.Errorf("resolving user for source: %w", err)
	}
	return userID, canonicalSourceID, nil
}
