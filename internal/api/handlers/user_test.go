package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/users"
)

// gAddressPattern matches a Stellar account id. The response must never carry
// one: the identity tables hold only hex ed25519 keys.
const gAddressPattern = `G[A-Z2-7]{55}`

// fakeUserStore records every call so tests can assert the handler makes
// exactly one ResolveUser and one ListSources call, with what.
type fakeUserStore struct {
	resolveCalls int
	resolveCtx   context.Context
	gotSource    string
	userID       uuid.UUID
	canonical    string
	resolveErr   error

	listCalls int
	listCtx   context.Context
	gotUserID uuid.UUID
	sources   []users.LinkedSource
	listErr   error
}

func (f *fakeUserStore) ResolveUser(ctx context.Context, sourceID string) (uuid.UUID, string, error) {
	f.resolveCalls++
	f.resolveCtx = ctx
	f.gotSource = sourceID
	if f.resolveErr != nil {
		return uuid.Nil, "", f.resolveErr
	}
	return f.userID, f.canonical, nil
}

func (f *fakeUserStore) ListSources(ctx context.Context, userID uuid.UUID) ([]users.LinkedSource, error) {
	f.listCalls++
	f.listCtx = ctx
	f.gotUserID = userID
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.sources, nil
}

// doGetUser runs the handler as the auth middleware would present the request:
// sub in the context (none when sub is empty). A nil store is passed through
// as a true nil interface (the DB-disabled wiring).
func doGetUser(t *testing.T, store *fakeUserStore, sub string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	var h *UserHandler
	if store == nil {
		h = NewUserHandler(nil)
	} else {
		h = NewUserHandler(store)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/user", nil)
	if sub != "" {
		r = r.WithContext(auth.ContextWithSourceID(r.Context(), sub))
	}
	w := httptest.NewRecorder()
	return w, h.GetUser(w, r)
}

func TestGetUser_Success(t *testing.T) {
	P, k1, k2 := newLinkKey(t), newLinkKey(t), newLinkKey(t)
	userID := uuid.New()
	created := time.Unix(1700000010, 0).UTC()
	store := &fakeUserStore{
		userID:    userID,
		canonical: P.id,
		sources: []users.LinkedSource{
			{ID: P.id, Kind: auth.SourceKindPhrase, CreatedAt: created},
			{ID: k1.id, Kind: auth.SourceKindSecretKey, CreatedAt: created},
			{ID: k2.id, Kind: auth.SourceKindSecretKey, CreatedAt: created.Add(time.Minute)},
		},
	}

	// Signed as k2, a member that is not the cluster root.
	w, err := doGetUser(t, store, k2.id)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp struct {
		Data UserResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, P.id, resp.Data.UserID, "userId is the canonical source id, not the sub")
	assert.Equal(t, []UserLinkedSource{
		{ID: P.id, Kind: auth.SourceKindPhrase, CreatedAt: created},
		{ID: k1.id, Kind: auth.SourceKindSecretKey, CreatedAt: created},
		{ID: k2.id, Kind: auth.SourceKindSecretKey, CreatedAt: created.Add(time.Minute)},
	}, resp.Data.Sources)

	// Exactly one resolver call and one sources query, chained on the resolved id.
	assert.Equal(t, 1, store.resolveCalls)
	assert.Equal(t, k2.id, store.gotSource)
	assert.Equal(t, 1, store.listCalls)
	assert.Equal(t, userID, store.gotUserID)

	// The internal users.id is a foreign key, not an identity; it never leaves
	// the server. And no G address, by construction.
	assert.NotContains(t, w.Body.String(), userID.String())
	assert.NotRegexp(t, gAddressPattern, w.Body.String())
	assert.NotContains(t, w.Body.String(), "authenticated", "this is the resolved view, not whoami")
}

func TestGetUser_UnlinkedSourceIs404(t *testing.T) {
	K := newLinkKey(t)
	store := &fakeUserStore{resolveErr: users.ErrSourceNotFound}

	_, err := doGetUser(t, store, K.id)
	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
	assert.Equal(t, 1, store.resolveCalls)
	assert.Equal(t, 0, store.listCalls, "nothing to list without a user")
	assert.NotContains(t, err.Error(), K.id, "the message never echoes the source id")
}

func TestGetUser_NoAuthenticatedSource(t *testing.T) {
	store := &fakeUserStore{}

	_, err := doGetUser(t, store, "")
	require.Error(t, err)
	assert.Equal(t, http.StatusUnauthorized, statusOf(t, err))
	assert.Equal(t, 0, store.resolveCalls)
	assert.Equal(t, 0, store.listCalls)
}

func TestGetUser_DatabaseDisabled(t *testing.T) {
	_, err := doGetUser(t, nil, newLinkKey(t).id)
	require.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, statusOf(t, err))
}

func TestGetUser_StoreErrorStatuses(t *testing.T) {
	dbErr := errors.New("connection reset")
	for _, tc := range []struct {
		err  error
		want int
	}{
		{context.DeadlineExceeded, http.StatusGatewayTimeout},
		{context.Canceled, http.StatusServiceUnavailable},
		{dbErr, http.StatusInternalServerError},
	} {
		wrapped := fmt.Errorf("store: %w", tc.err)

		// Injected on the resolve step.
		store := &fakeUserStore{resolveErr: wrapped}
		_, err := doGetUser(t, store, newLinkKey(t).id)
		require.Error(t, err)
		assert.Equal(t, tc.want, statusOf(t, err), "resolve: %v", tc.err)
		assert.Equal(t, 0, store.listCalls, "resolve: %v: a failed resolve must not list", tc.err)
		assert.NotContains(t, err.Error(), "connection reset", "resolve: the DB detail is not echoed")

		// Injected on the list step.
		store = &fakeUserStore{userID: uuid.New(), canonical: "aa", listErr: wrapped}
		_, err = doGetUser(t, store, newLinkKey(t).id)
		require.Error(t, err)
		assert.Equal(t, tc.want, statusOf(t, err), "list: %v", tc.err)
		assert.Equal(t, 1, store.listCalls, "list: %v", tc.err)
		assert.NotContains(t, err.Error(), "connection reset", "list: the DB detail is not echoed")
	}
}

// A resolved user with no sources cannot exist (the sub's own row is under it
// and rows are never deleted), so the handler treats it as a server fault.
func TestGetUser_EmptyClusterIs500(t *testing.T) {
	store := &fakeUserStore{userID: uuid.New(), canonical: "aa", sources: []users.LinkedSource{}}

	_, err := doGetUser(t, store, newLinkKey(t).id)
	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, statusOf(t, err))
}

// Both reads run under UserRequestTimeout: the request context carries no
// deadline of its own, so this is the only thing bounding a stuck read.
func TestGetUser_StoreGetsRequestDeadline(t *testing.T) {
	store := &fakeUserStore{
		userID: uuid.New(), canonical: "aa",
		sources: []users.LinkedSource{{ID: "aa", Kind: auth.SourceKindPhrase, CreatedAt: time.Now()}},
	}

	before := time.Now()
	_, err := doGetUser(t, store, newLinkKey(t).id)
	require.NoError(t, err)

	for name, ctx := range map[string]context.Context{"resolve": store.resolveCtx, "list": store.listCtx} {
		require.NotNil(t, ctx, name)
		deadline, ok := ctx.Deadline()
		require.True(t, ok, "%s: the store context must carry a deadline", name)
		assert.WithinDuration(t, before.Add(UserRequestTimeout), deadline, 2*time.Second, name)
	}
}
