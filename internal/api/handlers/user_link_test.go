package handlers

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/users"
)

// linkKey is a test source: its canonical id and the private key that consents.
type linkKey struct {
	id   string
	priv ed25519.PrivateKey
}

func newLinkKey(t *testing.T) linkKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return linkKey{id: hex.EncodeToString(pub), priv: priv}
}

// consent signs k's v2 consent for kind, bound to signer and iat.
func (k linkKey) consent(kind, signer string, iat time.Time) UserLinkSource {
	sig := ed25519.Sign(k.priv, auth.LinkConsentMessage(k.id, kind, signer, iat))
	return UserLinkSource{ID: k.id, Kind: kind, Sig: base64.StdEncoding.EncodeToString(sig)}
}

// fakeLinker records the one call it expects. A test that must write nothing
// leaves it at its zero value and asserts calls == 0 afterwards.
type fakeLinker struct {
	calls   int
	signer  string
	sources []users.Source
	result  *users.LinkResult
	err     error
}

func (f *fakeLinker) Link(_ context.Context, signer string, sources []users.Source) (*users.LinkResult, error) {
	f.calls++
	f.signer = signer
	f.sources = sources
	return f.result, f.err
}

// doLink runs the handler as the auth middleware would present the request:
// sub and iat in the context, body as JSON. A nil linker is passed through as a
// true nil interface (the DB-disabled wiring).
func doLink(t *testing.T, linker *fakeLinker, sub string, iat time.Time, body any) (*httptest.ResponseRecorder, error) {
	t.Helper()
	var h *UserLinkHandler
	if linker == nil {
		h = NewUserLinkHandler(nil)
	} else {
		h = NewUserLinkHandler(linker)
	}
	var raw []byte
	switch b := body.(type) {
	case string:
		raw = []byte(b)
	default:
		var err error
		raw, err = json.Marshal(b)
		require.NoError(t, err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/user/link", bytes.NewReader(raw))
	if sub != "" {
		ctx := auth.ContextWithSourceID(r.Context(), sub)
		ctx = auth.ContextWithIssuedAt(ctx, iat)
		r = r.WithContext(ctx)
	}
	w := httptest.NewRecorder()
	return w, h.Link(w, r)
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var he *httperror.HttpError
	require.ErrorAs(t, err, &he)
	return he.StatusCode
}

func TestUserLink_Success(t *testing.T) {
	iat := time.Unix(1700000000, 0)
	P, K := newLinkKey(t), newLinkKey(t)
	userID := uuid.New()
	created := time.Unix(1700000010, 0).UTC()
	linker := &fakeLinker{result: &users.LinkResult{
		UserID:            userID,
		CanonicalSourceID: P.id,
		Sources: []users.LinkedSource{
			{ID: P.id, Kind: auth.SourceKindPhrase, CreatedAt: created},
			{ID: K.id, Kind: auth.SourceKindSecretKey, CreatedAt: created},
		},
		Conflicts: []string{},
	}}

	w, err := doLink(t, linker, P.id, iat, UserLinkRequest{Sources: []UserLinkSource{
		P.consent(auth.SourceKindPhrase, P.id, iat),
		K.consent(auth.SourceKindSecretKey, P.id, iat),
	}})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)

	// The store is called with the JWT sub as signer and the kinds read from the
	// signed consents, in body order.
	assert.Equal(t, 1, linker.calls)
	assert.Equal(t, P.id, linker.signer)
	assert.Equal(t, []users.Source{
		{ID: P.id, Kind: auth.SourceKindPhrase},
		{ID: K.id, Kind: auth.SourceKindSecretKey},
	}, linker.sources)

	var resp struct {
		Data UserLinkResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, P.id, resp.Data.UserID, "userId is the canonical source id, not the internal uuid")
	assert.NotContains(t, w.Body.String(), userID.String(), "the internal users.id is never exposed")
	assert.Len(t, resp.Data.Sources, 2)
	assert.Equal(t, K.id, resp.Data.Sources[1].ID)
	assert.Equal(t, auth.SourceKindSecretKey, resp.Data.Sources[1].Kind)
	assert.Equal(t, created, resp.Data.Sources[1].CreatedAt.UTC())
	assert.NotNil(t, resp.Data.Conflicts)
	assert.Contains(t, w.Body.String(), `"conflicts":[]`, "an empty conflicts list is [] not null")
}

func TestUserLink_ConflictsAreReported(t *testing.T) {
	iat := time.Unix(1700000000, 0)
	P, K := newLinkKey(t), newLinkKey(t)
	linker := &fakeLinker{result: &users.LinkResult{
		CanonicalSourceID: P.id,
		Sources:           []users.LinkedSource{{ID: P.id, Kind: auth.SourceKindPhrase}},
		Conflicts:         []string{K.id},
	}}
	w, err := doLink(t, linker, P.id, iat, UserLinkRequest{Sources: []UserLinkSource{
		P.consent(auth.SourceKindPhrase, P.id, iat),
		K.consent(auth.SourceKindSecretKey, P.id, iat),
	}})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code, "a conflict is a 200 with the id listed, not an error")
	var resp struct {
		Data UserLinkResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, []string{K.id}, resp.Data.Conflicts)
}

// Every rejection must reach the store zero times: a rejected request writes
// nothing.
func TestUserLink_RejectedWritesNothing(t *testing.T) {
	iat := time.Unix(1700000000, 0)
	P, K, other := newLinkKey(t), newLinkKey(t), newLinkKey(t)
	valid := func() []UserLinkSource {
		return []UserLinkSource{
			P.consent(auth.SourceKindPhrase, P.id, iat),
			K.consent(auth.SourceKindSecretKey, P.id, iat),
		}
	}
	tamperSig := func(s UserLinkSource) UserLinkSource {
		raw, _ := base64.StdEncoding.DecodeString(s.Sig)
		raw[0] ^= 0x01
		s.Sig = base64.StdEncoding.EncodeToString(raw)
		return s
	}
	tooMany := func() []UserLinkSource {
		out := valid()
		for len(out) <= users.MaxLinkSources {
			out = append(out, newLinkKey(t).consent(auth.SourceKindSecretKey, P.id, iat))
		}
		return out
	}

	cases := []struct {
		name       string
		sub        string
		iat        time.Time
		body       any
		wantStatus int
		wantMsg    string
	}{
		{name: "bad signature", sub: P.id, iat: iat, body: UserLinkRequest{Sources: []UserLinkSource{valid()[0], tamperSig(valid()[1])}}, wantStatus: http.StatusForbidden, wantMsg: "sources[1]: consent does not verify"},
		{name: "forged: signed by another key", sub: P.id, iat: iat, body: UserLinkRequest{Sources: []UserLinkSource{valid()[0], {ID: K.id, Kind: auth.SourceKindSecretKey, Sig: other.consent(auth.SourceKindSecretKey, P.id, iat).Sig}}}, wantStatus: http.StatusForbidden},
		{name: "signingSourceId != sub", sub: P.id, iat: iat, body: UserLinkRequest{Sources: []UserLinkSource{P.consent(auth.SourceKindPhrase, P.id, iat), K.consent(auth.SourceKindSecretKey, other.id, iat)}}, wantStatus: http.StatusForbidden},
		{name: "consent for the sub itself signed for another signer", sub: P.id, iat: iat, body: UserLinkRequest{Sources: []UserLinkSource{P.consent(auth.SourceKindPhrase, other.id, iat)}}, wantStatus: http.StatusForbidden},
		{name: "iat from a different token", sub: P.id, iat: iat.Add(time.Second), body: UserLinkRequest{Sources: valid()}, wantStatus: http.StatusForbidden},
		{name: "kind changed after signing", sub: P.id, iat: iat, body: UserLinkRequest{Sources: []UserLinkSource{valid()[0], {ID: K.id, Kind: auth.SourceKindPhrase, Sig: valid()[1].Sig}}}, wantStatus: http.StatusForbidden},
		{name: "sub not in sources", sub: other.id, iat: iat, body: UserLinkRequest{Sources: valid()}, wantStatus: http.StatusBadRequest, wantMsg: "must include the authenticated source"},
		{name: "repeated id", sub: P.id, iat: iat, body: UserLinkRequest{Sources: []UserLinkSource{valid()[0], valid()[1], valid()[1]}}, wantStatus: http.StatusBadRequest, wantMsg: "sources[2].id is repeated"},
		{name: "two consents for one id with different kinds", sub: P.id, iat: iat, body: UserLinkRequest{Sources: []UserLinkSource{valid()[0], K.consent(auth.SourceKindSecretKey, P.id, iat), K.consent(auth.SourceKindPhrase, P.id, iat)}}, wantStatus: http.StatusBadRequest, wantMsg: "sources[2].id is repeated"},
		{name: "more than 128 sources", sub: P.id, iat: iat, body: UserLinkRequest{Sources: tooMany()}, wantStatus: http.StatusBadRequest, wantMsg: "too many sources"},
		{name: "unknown kind", sub: P.id, iat: iat, body: UserLinkRequest{Sources: []UserLinkSource{valid()[0], K.consent("hardware", P.id, iat)}}, wantStatus: http.StatusBadRequest, wantMsg: "sources[1].kind"},
		{name: "uppercase id is not canonical", sub: P.id, iat: iat, body: UserLinkRequest{Sources: []UserLinkSource{valid()[0], {ID: strings.ToUpper(K.id), Kind: auth.SourceKindSecretKey, Sig: valid()[1].Sig}}}, wantStatus: http.StatusBadRequest, wantMsg: "sources[1].id must be"},
		{name: "empty sources", sub: P.id, iat: iat, body: UserLinkRequest{}, wantStatus: http.StatusBadRequest, wantMsg: "must not be empty"},
		{name: "malformed JSON", sub: P.id, iat: iat, body: `{"sources": [`, wantStatus: http.StatusBadRequest, wantMsg: "invalid JSON"},
		{name: "no authenticated source", sub: "", iat: iat, body: UserLinkRequest{Sources: valid()}, wantStatus: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			linker := &fakeLinker{}
			_, err := doLink(t, linker, tc.sub, tc.iat, tc.body)
			require.Error(t, err)
			assert.Equal(t, tc.wantStatus, statusOf(t, err))
			if tc.wantMsg != "" {
				assert.Contains(t, err.Error(), tc.wantMsg)
			}
			assert.Zero(t, linker.calls, "a rejected request must never reach the store")
		})
	}
}

func TestUserLink_DatabaseDisabled(t *testing.T) {
	iat := time.Unix(1700000000, 0)
	P := newLinkKey(t)
	_, err := doLink(t, nil, P.id, iat, UserLinkRequest{Sources: []UserLinkSource{P.consent(auth.SourceKindPhrase, P.id, iat)}})
	require.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, statusOf(t, err))
}

func TestUserLink_StoreErrorIs500(t *testing.T) {
	iat := time.Unix(1700000000, 0)
	P := newLinkKey(t)
	linker := &fakeLinker{err: errors.New("connection reset")}
	_, err := doLink(t, linker, P.id, iat, UserLinkRequest{Sources: []UserLinkSource{P.consent(auth.SourceKindPhrase, P.id, iat)}})
	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, statusOf(t, err))
	assert.NotContains(t, err.Error(), "connection reset", "internal detail is not echoed to the client")
}

// The body is never a source of the signer or the issue time: a body that
// carries them is accepted and they are ignored in favor of the context.
func TestUserLink_IgnoresBodySignerAndIat(t *testing.T) {
	iat := time.Unix(1700000000, 0)
	P := newLinkKey(t)
	linker := &fakeLinker{result: &users.LinkResult{CanonicalSourceID: P.id, Sources: []users.LinkedSource{{ID: P.id, Kind: auth.SourceKindPhrase}}, Conflicts: []string{}}}
	c := P.consent(auth.SourceKindPhrase, P.id, iat)
	body := map[string]any{
		"signingSourceId": newLinkKey(t).id,
		"iat":             1,
		"sources":         []UserLinkSource{c},
	}
	w, err := doLink(t, linker, P.id, iat, body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, P.id, linker.signer)
}
