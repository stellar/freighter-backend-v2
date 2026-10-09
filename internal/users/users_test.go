package users

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Both the pool and a transaction must be usable as the Store's backend: the
// link handler resolves the caller inside its own transaction.
var (
	_ Querier = (*pgxpool.Pool)(nil)
	_ Querier = (pgx.Tx)(nil)
)

// fakeRow stands in for pgx.Row. On Scan it either returns err or writes the
// configured user id and canonical source id into the two destinations.
type fakeRow struct {
	userID    uuid.UUID
	canonical string
	err       error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*uuid.UUID) = r.userID
	*dest[1].(*string) = r.canonical
	return nil
}

// fakeQuerier records the query it was asked and returns a fixed row.
type fakeQuerier struct {
	row     fakeRow
	gotSQL  string
	gotArgs []any
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.gotSQL = sql
	q.gotArgs = args
	return q.row
}

func TestResolveUser_KnownSource(t *testing.T) {
	want := uuid.New()
	q := &fakeQuerier{row: fakeRow{userID: want, canonical: "aa"}}

	userID, canonical, err := NewStore(q).ResolveUser(t.Context(), "bb")
	require.NoError(t, err)
	assert.Equal(t, want, userID)
	assert.Equal(t, "aa", canonical, "the canonical source id is the user's, not the source queried")

	// The source id is bound as the single parameter, never interpolated.
	assert.Equal(t, resolveUserSQL, q.gotSQL)
	assert.Equal(t, []any{"bb"}, q.gotArgs)
}

func TestResolveUser_UnknownSourceIsSentinel(t *testing.T) {
	q := &fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}

	userID, canonical, err := NewStore(q).ResolveUser(t.Context(), "cc")
	require.ErrorIs(t, err, ErrSourceNotFound)
	assert.Equal(t, uuid.Nil, userID)
	assert.Empty(t, canonical)
}

func TestResolveUser_OtherErrorIsNotNotFound(t *testing.T) {
	dbErr := errors.New("connection reset")
	q := &fakeQuerier{row: fakeRow{err: dbErr}}

	_, _, err := NewStore(q).ResolveUser(t.Context(), "cc")
	require.Error(t, err)
	// An outage must surface as operational (500), never as "no such user" (404).
	assert.NotErrorIs(t, err, ErrSourceNotFound)
	assert.ErrorIs(t, err, dbErr)
}
