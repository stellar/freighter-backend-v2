package users

import (
	"context"
	"errors"
	"testing"
	"time"

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

// fakeRows stands in for pgx.Rows. It embeds the interface and overrides only
// what pgx.CollectRows touches: Next, Scan, Err, Close. Each row is the three
// columns of listSourcesSQL in order.
type fakeRows struct {
	pgx.Rows
	rows [][]any
	i    int
	err  error // returned by Err after the rows are exhausted
}

func (r *fakeRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	row := r.rows[r.i-1]
	*dest[0].(*string) = row[0].(string)
	*dest[1].(*string) = row[1].(string)
	*dest[2].(*time.Time) = row[2].(time.Time)
	return nil
}

func (r *fakeRows) Err() error { return r.err }
func (r *fakeRows) Close()     {}

// fakeQuerier records the query it was asked and returns a fixed row (QueryRow)
// or fixed rows (Query).
type fakeQuerier struct {
	row      fakeRow
	rows     *fakeRows
	queryErr error
	gotSQL   string
	gotArgs  []any
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.gotSQL = sql
	q.gotArgs = args
	return q.row
}

func (q *fakeQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.gotSQL = sql
	q.gotArgs = args
	if q.queryErr != nil {
		return nil, q.queryErr
	}
	return q.rows, nil
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

func TestListSources_ReturnsRowsInOrder(t *testing.T) {
	userID := uuid.New()
	t1 := time.Unix(1700000000, 0).UTC()
	t2 := t1.Add(time.Minute)
	q := &fakeQuerier{rows: &fakeRows{rows: [][]any{
		{"aa", "phrase", t1},
		{"bb", "secret_key", t2},
	}}}

	got, err := NewStore(q).ListSources(t.Context(), userID)
	require.NoError(t, err)
	assert.Equal(t, []LinkedSource{
		{ID: "aa", Kind: "phrase", CreatedAt: t1},
		{ID: "bb", Kind: "secret_key", CreatedAt: t2},
	}, got)

	// The user id is bound as the single parameter, never interpolated, and the
	// query is the shared one Link reads the cluster through.
	assert.Equal(t, listSourcesSQL, q.gotSQL)
	assert.Equal(t, []any{userID}, q.gotArgs)
}

func TestListSources_EmptyIsNotNil(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{}}

	got, err := NewStore(q).ListSources(t.Context(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got, "an empty cluster must marshal as [] rather than null")
	assert.Empty(t, got)
}

func TestListSources_QueryErrorIsWrapped(t *testing.T) {
	dbErr := errors.New("connection reset")
	q := &fakeQuerier{queryErr: dbErr}

	_, err := NewStore(q).ListSources(t.Context(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, dbErr)
	assert.NotErrorIs(t, err, ErrSourceNotFound, "an outage is operational, never not-found")
}

func TestListSources_RowsErrIsWrapped(t *testing.T) {
	dbErr := errors.New("connection reset mid-stream")
	q := &fakeQuerier{rows: &fakeRows{rows: [][]any{{"aa", "phrase", time.Now()}}, err: dbErr}}

	_, err := NewStore(q).ListSources(t.Context(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, dbErr)
	assert.NotErrorIs(t, err, ErrSourceNotFound)
}
