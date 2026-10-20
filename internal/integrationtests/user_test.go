// ABOUTME: Integration test for GET /api/v1/user against the real backend
// ABOUTME: container: the resolved identity is the same from every source.

package integrationtests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/stellar/freighter-backend-v2/internal/api/handlers"
	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/auth/authtest"
	"github.com/stellar/freighter-backend-v2/internal/integrationtests/infrastructure"
)

// gAddressPattern matches a Stellar account id, which the response must never
// carry.
const gAddressPattern = `G[A-Z2-7]{55}`

// UserTestSuite exercises the full wiring of GET /api/v1/user: the Required
// auth wrap under the container's default (permissive) AUTH_MODE, resolution
// of the JWT sub through the migrated app database, and the client contract
// that every source of a cluster reads back the same identity.
type UserTestSuite struct {
	suite.Suite
	freighterContainer *infrastructure.FreighterBackendContainer
	baseURL            string
}

func (s *UserTestSuite) SetupSuite() {
	var err error
	s.baseURL, err = s.freighterContainer.GetConnectionString(context.Background())
	s.Require().NoError(err)
}

// getUserAs sends GET /api/v1/user with a JWT signed by signer.
func (s *UserTestSuite) getUserAs(signer linkKey) (int, []byte) {
	t := s.T()
	token := authtest.MintToken(t, signer.priv, signer.id, "GET /api/v1/user", auth.MaxTokenLifetime, time.Now(), nil)
	req, err := http.NewRequest(http.MethodGet, s.baseURL+"/api/v1/user", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

// After a link signed as phrase P with account keys k1 and k2, a request
// signed as P returns userId = P and three sources, and a request signed as k2
// (a member that is not the cluster root) returns the same body.
func (s *UserTestSuite) TestGetUserFromAnySourceReturnsCanonicalIdAndAllSources() {
	t := s.T()
	P, k1, k2 := newLinkKey(t), newLinkKey(t), newLinkKey(t)

	iat := time.Now().Truncate(time.Second)
	status, raw := postLink(t, s.baseURL, P, iat,
		P.consent(auth.SourceKindPhrase, P.id, iat),
		k1.consent(auth.SourceKindSecretKey, P.id, iat),
		k2.consent(auth.SourceKindSecretKey, P.id, iat),
	)
	require.Equal(t, http.StatusOK, status, "link body: %s", raw)

	status, bodyP := s.getUserAs(P)
	require.Equal(t, http.StatusOK, status, "body: %s", bodyP)
	var envelope struct {
		Data handlers.UserResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(bodyP, &envelope), "body: %s", bodyP)
	require.Equal(t, P.id, envelope.Data.UserID)
	require.Len(t, envelope.Data.Sources, 3)
	kinds := map[string]string{}
	for _, src := range envelope.Data.Sources {
		kinds[src.ID] = src.Kind
		require.False(t, src.CreatedAt.IsZero(), "source %s has no createdAt", src.ID)
	}
	require.Equal(t, map[string]string{
		P.id:  auth.SourceKindPhrase,
		k1.id: auth.SourceKindSecretKey,
		k2.id: auth.SourceKindSecretKey,
	}, kinds)
	require.NotRegexp(t, gAddressPattern, string(bodyP))
	require.NotContains(t, string(bodyP), "authenticated", "this is the resolved view, not whoami")

	// Signed as k2: same user, same sources, byte for byte the same data.
	status, bodyK2 := s.getUserAs(k2)
	require.Equal(t, http.StatusOK, status, "body: %s", bodyK2)
	require.JSONEq(t, string(bodyP), string(bodyK2), "every source of a cluster reads back the same identity")
}

// A source nobody has linked is not an error on the client's side: it uses
// its pinned source id locally until it links.
func (s *UserTestSuite) TestUnlinkedSourceIs404() {
	t := s.T()
	status, raw := s.getUserAs(newLinkKey(t))
	require.Equal(t, http.StatusNotFound, status, "body: %s", raw)
}

// The container runs the default permissive AUTH_MODE, under which every other
// gated route serves anonymous requests. The user route must not.
func (s *UserTestSuite) TestAnonymousIsRejectedUnderPermissiveMode() {
	t := s.T()
	resp, err := http.Get(s.baseURL + "/api/v1/user")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Control: a gated-but-not-required route serves the same anonymous caller.
	ctrl, err := http.Get(s.baseURL + "/api/v1/auth/whoami")
	require.NoError(t, err)
	defer func() { _ = ctrl.Body.Close() }()
	require.Equal(t, http.StatusOK, ctrl.StatusCode, "the global mode is permissive; user's 401 comes from its own Required wrap")
}
