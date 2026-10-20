// ABOUTME: Integration test for POST /api/v1/user/link against the real
// ABOUTME: backend container and its migrated app Postgres.

package integrationtests

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/stellar/freighter-backend-v2/internal/api/handlers"
	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/auth/authtest"
	"github.com/stellar/freighter-backend-v2/internal/integrationtests/infrastructure"
)

// UserLinkTestSuite exercises the full wiring of the link endpoint: the
// Required auth wrap under the container's default (permissive) AUTH_MODE, the
// JWT-bound consent verification with the iat taken from the token, and the
// rows the handler writes to the app database.
type UserLinkTestSuite struct {
	suite.Suite
	freighterContainer   *infrastructure.FreighterBackendContainer
	appPostgresContainer *infrastructure.TestContainer
	baseURL              string
	metricsURL           string
	db                   *pgx.Conn
}

func (s *UserLinkTestSuite) SetupSuite() {
	ctx := context.Background()
	var err error
	s.baseURL, err = s.freighterContainer.GetConnectionString(ctx)
	s.Require().NoError(err)
	s.metricsURL, err = s.freighterContainer.GetMetricsConnectionString(ctx)
	s.Require().NoError(err)

	dsn, err := infrastructure.AppDatabaseHostURL(ctx, s.appPostgresContainer)
	s.Require().NoError(err)
	s.db, err = pgx.Connect(ctx, dsn)
	s.Require().NoError(err)
}

func (s *UserLinkTestSuite) TearDownSuite() {
	if s.db != nil {
		_ = s.db.Close(context.Background())
	}
}

type linkKey struct {
	id   string
	priv ed25519.PrivateKey
}

func newLinkKey(t require.TestingT) linkKey {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return linkKey{id: hex.EncodeToString(pub), priv: priv}
}

func (k linkKey) consent(kind, signer string, iat time.Time) handlers.UserLinkSource {
	sig := ed25519.Sign(k.priv, auth.LinkConsentMessage(k.id, kind, signer, iat))
	return handlers.UserLinkSource{ID: k.id, Kind: kind, Sig: base64.StdEncoding.EncodeToString(sig)}
}

// postLinkAt sends the request as signer with a JWT minted for exactly this
// body and issued at iat. Callers sign their consents over the same iat: the
// consent message carries it as unix seconds and so does the JWT numeric date,
// so the two must be the same whole second or the server, which reads iat back
// from the token, rebuilds a different message than the one signed.
func (s *UserLinkTestSuite) postLinkAt(signer linkKey, iat time.Time, sources ...handlers.UserLinkSource) (int, []byte) {
	t := s.T()
	body, err := json.Marshal(handlers.UserLinkRequest{Sources: sources})
	require.NoError(t, err)
	token := authtest.MintToken(t, signer.priv, signer.id, "POST /api/v1/user/link", auth.MaxTokenLifetime, iat, body)

	req, err := http.NewRequest(http.MethodPost, s.baseURL+"/api/v1/user/link", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

func (s *UserLinkTestSuite) ownerOf(id string) (userID string, canonical string) {
	t := s.T()
	err := s.db.QueryRow(context.Background(),
		`SELECT u.id::text, u.canonical_source_id FROM user_sources AS s JOIN users AS u ON u.id = s.user_id WHERE s.source_id = $1`, id).
		Scan(&userID, &canonical)
	require.NoError(t, err, "source %s has no row", id)
	return userID, canonical
}

// The container runs the default permissive AUTH_MODE, under which every other
// gated route serves anonymous requests. The link route must not.
func (s *UserLinkTestSuite) TestAnonymousIsRejectedUnderPermissiveMode() {
	t := s.T()
	resp, err := http.Post(s.baseURL+"/api/v1/user/link", "application/json", bytes.NewReader([]byte(`{"sources":[]}`)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Control: a gated-but-not-required route serves the same anonymous caller.
	ctrl, err := http.Get(s.baseURL + "/api/v1/auth/whoami")
	require.NoError(t, err)
	defer func() { _ = ctrl.Body.Close() }()
	require.Equal(t, http.StatusOK, ctrl.StatusCode, "the global mode is permissive; link's 401 comes from its own Required wrap")
}

func (s *UserLinkTestSuite) TestLinkCreatesClusterThenAttachesAndReportsConflicts() {
	t := s.T()
	P, k1, k2 := newLinkKey(t), newLinkKey(t), newLinkKey(t)

	// A fresh wallet links its phrase and one key, signed as the phrase.
	iat := time.Now().Truncate(time.Second)
	status, raw := s.postLinkAt(P, iat,
		P.consent(auth.SourceKindPhrase, P.id, iat),
		k1.consent(auth.SourceKindSecretKey, P.id, iat),
	)
	require.Equal(t, http.StatusOK, status, "body: %s", raw)
	var envelope struct {
		Data handlers.UserLinkResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope), "body: %s", raw)
	require.Equal(t, P.id, envelope.Data.UserID)
	require.Len(t, envelope.Data.Sources, 2)
	require.Empty(t, envelope.Data.Conflicts)
	require.NotNil(t, envelope.Data.Conflicts)

	pUser, pCanonical := s.ownerOf(P.id)
	k1User, _ := s.ownerOf(k1.id)
	require.Equal(t, pUser, k1User)
	require.Equal(t, P.id, pCanonical)

	// A different wallet that holds k1 and a new key k2 signs as k2: k2 roots a
	// new cluster and k1, already claimed, comes back as a conflict and stays put.
	iat = time.Now().Truncate(time.Second)
	status, raw = s.postLinkAt(k2, iat,
		k2.consent(auth.SourceKindSecretKey, k2.id, iat),
		k1.consent(auth.SourceKindSecretKey, k2.id, iat),
	)
	require.Equal(t, http.StatusOK, status, "body: %s", raw)
	require.NoError(t, json.Unmarshal(raw, &envelope), "body: %s", raw)
	require.Equal(t, k2.id, envelope.Data.UserID)
	require.Equal(t, []string{k1.id}, envelope.Data.Conflicts)

	k1UserAfter, _ := s.ownerOf(k1.id)
	require.Equal(t, pUser, k1UserAfter, "a claimed source is never moved")
	k2User, k2Canonical := s.ownerOf(k2.id)
	require.NotEqual(t, pUser, k2User)
	require.Equal(t, k2.id, k2Canonical)

	// The link metrics are exposed on the internal metrics server with the
	// pinned names and label values; other suites' link calls may have run too,
	// so assert presence, not exact counts.
	mresp, err := http.Get(s.metricsURL + "/metrics")
	require.NoError(t, err)
	defer func() { _ = mresp.Body.Close() }()
	require.Equal(t, http.StatusOK, mresp.StatusCode)
	mbody, err := io.ReadAll(mresp.Body)
	require.NoError(t, err)
	for _, want := range []string{
		`freighter_user_link_requests_total{result="created"}`,
		`freighter_user_link_sources_total{outcome="written"}`,
		`freighter_user_link_sources_total{outcome="conflict"}`,
	} {
		require.Contains(t, string(mbody), want)
	}
}

// A consent minted under a different token's iat fails: the server takes iat
// from the verified JWT, so a replayed consent cannot ride a fresh token.
func (s *UserLinkTestSuite) TestConsentBoundToTokenIat() {
	t := s.T()
	P := newLinkKey(t)
	iat := time.Now().Truncate(time.Second)
	stale := iat.Add(-5 * time.Second)
	status, raw := s.postLinkAt(P, iat, P.consent(auth.SourceKindPhrase, P.id, stale))
	require.Equal(t, http.StatusForbidden, status, "body: %s", raw)

	var n int
	require.NoError(t, s.db.QueryRow(context.Background(), `SELECT count(*) FROM user_sources WHERE source_id = $1`, P.id).Scan(&n))
	require.Zero(t, n, "a rejected request writes nothing")
}
