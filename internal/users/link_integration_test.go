package users

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/db"
)

// newID returns a fresh canonical source id (hex of 32 random bytes). The store
// never verifies signatures, so a random id stands in for a real public key.
func newID(t *testing.T) string {
	t.Helper()
	var b [32]byte
	_, err := rand.Read(b[:])
	require.NoError(t, err)
	return hex.EncodeToString(b[:])
}

func phrase(id string) Source    { return Source{ID: id, Kind: auth.SourceKindPhrase} }
func secretKey(id string) Source { return Source{ID: id, Kind: auth.SourceKindSecretKey} }

// linkDB wraps the pool with the row-level assertions every case needs.
type linkDB struct {
	t    *testing.T
	pool *pgxpool.Pool
}

// ownerOf returns the users.id that source id points at, failing if it has no row.
func (d linkDB) ownerOf(id string) uuid.UUID {
	d.t.Helper()
	var u uuid.UUID
	require.NoError(d.t, d.pool.QueryRow(context.Background(),
		`SELECT user_id FROM user_sources WHERE source_id = $1`, id).Scan(&u), "source %s has no row", id)
	return u
}

// hasRow reports whether source id has a user_sources row.
func (d linkDB) hasRow(id string) bool {
	d.t.Helper()
	var n int
	require.NoError(d.t, d.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM user_sources WHERE source_id = $1`, id).Scan(&n))
	return n == 1
}

// clusterIDs returns the source ids under user, sorted by creation.
func (d linkDB) clusterIDs(user uuid.UUID) []string {
	d.t.Helper()
	rows, err := d.pool.Query(context.Background(),
		`SELECT source_id FROM user_sources WHERE user_id = $1 ORDER BY created_at, source_id`, user)
	require.NoError(d.t, err)
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		require.NoError(d.t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(d.t, rows.Err())
	return ids
}

// canonicalOf returns users.canonical_source_id for user.
func (d linkDB) canonicalOf(user uuid.UUID) string {
	d.t.Helper()
	var c string
	require.NoError(d.t, d.pool.QueryRow(context.Background(),
		`SELECT canonical_source_id FROM users WHERE id = $1`, user).Scan(&c))
	return c
}

// usersWithCanonical counts users rows whose canonical_source_id is id.
func (d linkDB) usersWithCanonical(id string) int {
	d.t.Helper()
	var n int
	require.NoError(d.t, d.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM users WHERE canonical_source_id = $1`, id).Scan(&n))
	return n
}

// assertNoCrossClusterWrites is the invariant every conflict case must hold:
// each foreign source still points at its original owner, and every row the
// call wrote belongs to the caller's cluster. It deliberately does not assert
// that nothing changed: the conflict branch still writes the caller's unclaimed
// sources.
func (d linkDB) assertNoCrossClusterWrites(res *LinkResult, foreign map[string]uuid.UUID) {
	d.t.Helper()
	for id, owner := range foreign {
		assert.Equal(d.t, owner, d.ownerOf(id), "foreign source %s must still point at its original cluster", id)
		assert.Contains(d.t, res.Conflicts, id)
	}
	for _, s := range res.Sources {
		assert.Equal(d.t, res.UserID, d.ownerOf(s.ID), "returned source %s must belong to the caller's cluster", s.ID)
		assert.NotContains(d.t, res.Conflicts, s.ID, "a source cannot be both linked and in conflict")
	}
}

func idsOf(sources []LinkedSource) []string {
	out := make([]string, len(sources))
	for i, s := range sources {
		out[i] = s.ID
	}
	return out
}

// TestLink_Postgres runs the resolve-by-signer matrix, the canonical-root
// invariants, the concurrency cases, and the rejection cases of the user-link
// endpoint against a migrated Postgres. One container is shared by all
// subtests; every case uses fresh random ids, so cases cannot see each other
// and assertions are scoped to the rows each case touches.
func TestLink_Postgres(t *testing.T) {
	dsn := startMigratedPostgres(t)
	ctx := context.Background()
	pool, err := db.OpenDBConnectionPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	d := linkDB{t: t, pool: pool}
	linker := NewLinker(pool)

	// seed creates a cluster rooted at root with the given extra sources, through
	// the same code path a client would use, and returns its users.id.
	seed := func(t *testing.T, root Source, extra ...Source) uuid.UUID {
		t.Helper()
		res, err := linker.Link(ctx, root.ID, append([]Source{root}, extra...))
		require.NoError(t, err)
		require.Empty(t, res.Conflicts)
		return res.UserID
	}

	t.Run("matrix", func(t *testing.T) {
		t.Run("fresh wallet links {K, P} signed as P: P is a new cluster, K conflicts, U1 unchanged", func(t *testing.T) {
			K, P := newID(t), newID(t)
			u1 := seed(t, secretKey(K))

			res, err := linker.Link(ctx, P, []Source{secretKey(K), phrase(P)})
			require.NoError(t, err)
			assert.NotEqual(t, u1, res.UserID)
			assert.Equal(t, P, res.CanonicalSourceID)
			assert.Equal(t, []string{P}, idsOf(res.Sources))
			assert.Equal(t, []string{K}, res.Conflicts)
			assert.Equal(t, []string{K}, d.clusterIDs(u1), "U1 gained no row")
			d.assertNoCrossClusterWrites(res, map[string]uuid.UUID{K: u1})
		})

		t.Run("fresh wallet links {K} signed as K: resolves to U1; then {K, P2} signed as K attaches P2 to U1", func(t *testing.T) {
			K, P2 := newID(t), newID(t)
			u1 := seed(t, secretKey(K))

			res, err := linker.Link(ctx, K, []Source{secretKey(K)})
			require.NoError(t, err)
			assert.Equal(t, u1, res.UserID)
			assert.Equal(t, K, res.CanonicalSourceID)
			assert.Empty(t, res.Conflicts)
			assert.Equal(t, []string{K}, d.clusterIDs(u1))

			res, err = linker.Link(ctx, K, []Source{secretKey(K), phrase(P2)})
			require.NoError(t, err)
			assert.Equal(t, u1, res.UserID)
			assert.Empty(t, res.Conflicts)
			assert.ElementsMatch(t, []string{K, P2}, idsOf(res.Sources))
			assert.Equal(t, u1, d.ownerOf(P2))
			assert.Equal(t, K, d.canonicalOf(u1), "canonical id is unchanged by a later phrase")
		})

		t.Run("U2 holds phrase A: {A, K2} signed as A attaches K2 to U2", func(t *testing.T) {
			A, K2 := newID(t), newID(t)
			u2 := seed(t, phrase(A))

			res, err := linker.Link(ctx, A, []Source{phrase(A), secretKey(K2)})
			require.NoError(t, err)
			assert.Equal(t, u2, res.UserID)
			assert.Empty(t, res.Conflicts)
			assert.Equal(t, u2, d.ownerOf(K2))
			assert.ElementsMatch(t, []string{A, K2}, d.clusterIDs(u2))
		})

		t.Run("U2 holds phrase A: {A, K2} signed as K2 makes K2 a new cluster, A conflicts, U2 unchanged", func(t *testing.T) {
			A, K2 := newID(t), newID(t)
			u2 := seed(t, phrase(A))

			res, err := linker.Link(ctx, K2, []Source{phrase(A), secretKey(K2)})
			require.NoError(t, err)
			assert.NotEqual(t, u2, res.UserID)
			assert.Equal(t, K2, res.CanonicalSourceID)
			assert.Equal(t, []string{A}, res.Conflicts)
			assert.Equal(t, []string{A}, d.clusterIDs(u2), "U2 unchanged")
			d.assertNoCrossClusterWrites(res, map[string]uuid.UUID{A: u2})
		})

		t.Run("U1 holds A, k1, k2: {k2} signed as k2 resolves to U1", func(t *testing.T) {
			A, k1, k2 := newID(t), newID(t), newID(t)
			u1 := seed(t, phrase(A), secretKey(k1), secretKey(k2))

			res, err := linker.Link(ctx, k2, []Source{secretKey(k2)})
			require.NoError(t, err)
			assert.Equal(t, u1, res.UserID)
			assert.Equal(t, A, res.CanonicalSourceID, "resolves to the cluster's root, not the signer")
			assert.ElementsMatch(t, []string{A, k1, k2}, idsOf(res.Sources))
			assert.Empty(t, res.Conflicts)
		})

		t.Run("U1 holds A, k1, k2: {k2, P} signed as P makes P a new cluster, k2 conflicts, U1 unchanged", func(t *testing.T) {
			A, k1, k2, P := newID(t), newID(t), newID(t), newID(t)
			u1 := seed(t, phrase(A), secretKey(k1), secretKey(k2))

			res, err := linker.Link(ctx, P, []Source{secretKey(k2), phrase(P)})
			require.NoError(t, err)
			assert.NotEqual(t, u1, res.UserID)
			assert.Equal(t, P, res.CanonicalSourceID)
			assert.Equal(t, []string{k2}, res.Conflicts)
			assert.ElementsMatch(t, []string{A, k1, k2}, d.clusterIDs(u1), "U1 gains no row")
			d.assertNoCrossClusterWrites(res, map[string]uuid.UUID{k2: u1})
		})

		t.Run("key first, then phrase: {k2} as k2 owns UK; {A, k1, k2} as A forms a new cluster with k2 in conflicts, UK unchanged", func(t *testing.T) {
			A, k1, k2 := newID(t), newID(t), newID(t)
			uk := seed(t, secretKey(k2))

			res, err := linker.Link(ctx, A, []Source{phrase(A), secretKey(k1), secretKey(k2)})
			require.NoError(t, err)
			assert.NotEqual(t, uk, res.UserID)
			assert.Equal(t, A, res.CanonicalSourceID, "A is canonical")
			assert.ElementsMatch(t, []string{A, k1}, idsOf(res.Sources))
			assert.Equal(t, []string{k2}, res.Conflicts)
			assert.Equal(t, []string{k2}, d.clusterIDs(uk), "UK unchanged")
			assert.Equal(t, k2, d.canonicalOf(uk))
			d.assertNoCrossClusterWrites(res, map[string]uuid.UUID{k2: uk})
		})

		t.Run("foreign sources from two different users both come back in conflicts", func(t *testing.T) {
			A, B, P, K := newID(t), newID(t), newID(t), newID(t)
			ua := seed(t, phrase(A))
			ub := seed(t, phrase(B))

			res, err := linker.Link(ctx, P, []Source{phrase(P), phrase(A), phrase(B), secretKey(K)})
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{A, B}, res.Conflicts)
			assert.ElementsMatch(t, []string{P, K}, idsOf(res.Sources), "the caller's unclaimed sources are still written")
			assert.Equal(t, []string{A}, d.clusterIDs(ua))
			assert.Equal(t, []string{B}, d.clusterIDs(ub))
			d.assertNoCrossClusterWrites(res, map[string]uuid.UUID{A: ua, B: ub})
		})
	})

	t.Run("canonical id is the root", func(t *testing.T) {
		P, k1, k2, k3 := newID(t), newID(t), newID(t), newID(t)
		u := seed(t, phrase(P), secretKey(k1), secretKey(k2), secretKey(k3))
		assert.Equal(t, P, d.canonicalOf(u))

		// An empty wallet signing in as k2 changes nothing.
		res, err := linker.Link(ctx, k2, []Source{secretKey(k2)})
		require.NoError(t, err)
		assert.Equal(t, u, res.UserID)
		assert.Equal(t, P, d.canonicalOf(u))

		// A secret-key-only cluster that gains a phrase later keeps its key as root.
		k := newID(t)
		uk := seed(t, secretKey(k))
		late := newID(t)
		res, err = linker.Link(ctx, k, []Source{secretKey(k), phrase(late)})
		require.NoError(t, err)
		assert.Equal(t, uk, res.UserID)
		assert.Equal(t, k, res.CanonicalSourceID)
		assert.Equal(t, k, d.canonicalOf(uk), "attaching a phrase later never re-roots the cluster")

		var nulls int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE canonical_source_id IS NULL`).Scan(&nulls))
		assert.Zero(t, nulls)
	})

	t.Run("concurrency", func(t *testing.T) {
		t.Run("same signer: two callers sign as the same unclaimed source, exactly one users row", func(t *testing.T) {
			for i := 0; i < 100; i++ {
				S := newID(t)
				results := make([]*LinkResult, 2)
				errs := make([]error, 2)
				var wg sync.WaitGroup
				for c := 0; c < 2; c++ {
					wg.Add(1)
					go func(c int) {
						defer wg.Done()
						results[c], errs[c] = linker.Link(ctx, S, []Source{phrase(S)})
					}(c)
				}
				wg.Wait()
				for c := 0; c < 2; c++ {
					require.NoError(t, errs[c], "iteration %d caller %d", i, c)
				}
				assert.Equal(t, results[0].UserID, results[1].UserID, "iteration %d: both callers resolve to the same user", i)
				assert.Equal(t, 1, d.usersWithCanonical(S), "iteration %d: exactly one users row", i)
				assert.Equal(t, results[0].UserID, d.ownerOf(S))
			}
		})

		t.Run("different signers: P and Q both include new key K; two users rows, K under the first, conflict for the second", func(t *testing.T) {
			for i := 0; i < 20; i++ {
				P, Q, K := newID(t), newID(t), newID(t)
				results := make([]*LinkResult, 2)
				errs := make([]error, 2)
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					results[0], errs[0] = linker.Link(ctx, P, []Source{phrase(P), secretKey(K)})
				}()
				go func() {
					defer wg.Done()
					results[1], errs[1] = linker.Link(ctx, Q, []Source{phrase(Q), secretKey(K)})
				}()
				wg.Wait()
				require.NoError(t, errs[0])
				require.NoError(t, errs[1])
				assert.NotEqual(t, results[0].UserID, results[1].UserID)
				assert.Equal(t, 1, d.usersWithCanonical(P))
				assert.Equal(t, 1, d.usersWithCanonical(Q))

				// Whichever caller took the locks first owns K; the other sees it in conflicts.
				owner := d.ownerOf(K)
				var winner, loser *LinkResult
				if owner == results[0].UserID {
					winner, loser = results[0], results[1]
				} else {
					require.Equal(t, results[1].UserID, owner, "K must belong to one of the two callers")
					winner, loser = results[1], results[0]
				}
				assert.Empty(t, winner.Conflicts, "iteration %d", i)
				assert.Contains(t, idsOf(winner.Sources), K)
				assert.Equal(t, []string{K}, loser.Conflicts, "iteration %d", i)
				assert.NotContains(t, idsOf(loser.Sources), K)
			}
		})
	})

	t.Run("rejected, writing nothing", func(t *testing.T) {
		S, X := newID(t), newID(t)
		cases := []struct {
			name    string
			signer  string
			sources []Source
			wantErr error
		}{
			{"repeated id", S, []Source{phrase(S), phrase(S)}, ErrRepeatedSource},
			{"two consents for one id with different kinds", S, []Source{phrase(S), secretKey(S)}, ErrRepeatedSource},
			{"signer not in sources", S, []Source{phrase(X)}, ErrSignerNotInBody},
			{"unknown kind", S, []Source{{ID: S, Kind: "hardware"}}, ErrUnknownSourceKind},
			{"empty body", S, nil, ErrSignerNotInBody},
			{"more than 128 sources", S, func() []Source {
				out := []Source{phrase(S)}
				for len(out) <= MaxLinkSources {
					out = append(out, secretKey(newID(t)))
				}
				return out
			}(), ErrTooManySources},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := linker.Link(ctx, tc.signer, tc.sources)
				require.ErrorIs(t, err, tc.wantErr)
				assert.False(t, d.hasRow(S), "nothing written for the signer")
				assert.False(t, d.hasRow(X))
				assert.Zero(t, d.usersWithCanonical(S))
			})
		}
	})

	// hashtext collisions share a lock key; the sort+compact must not break on
	// a real collision, so exercise the lock path with a large, mixed batch.
	t.Run("full batch of 128 sources links in one call", func(t *testing.T) {
		P := newID(t)
		sources := []Source{phrase(P)}
		for len(sources) < MaxLinkSources {
			sources = append(sources, secretKey(newID(t)))
		}
		res, err := linker.Link(ctx, P, sources)
		require.NoError(t, err)
		assert.Len(t, res.Sources, MaxLinkSources)
		assert.Empty(t, res.Conflicts)
		assert.Equal(t, fmt.Sprint(MaxLinkSources), fmt.Sprint(len(d.clusterIDs(res.UserID))))
	})
}
