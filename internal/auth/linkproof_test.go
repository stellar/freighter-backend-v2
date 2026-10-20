package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSource generates a keypair and returns its canonical source id and private key.
func newSource(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return hex.EncodeToString(pub), priv
}

// signConsent signs the v2 consent for (id, kind) bound to signer/iat with priv.
func signConsent(id, kind, signer string, iat time.Time, priv ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, LinkConsentMessage(id, kind, signer, iat)))
}

func TestLinkConsentMessage_Format(t *testing.T) {
	iat := time.Unix(1700000000, 999_999_999) // sub-second part must be dropped
	got := LinkConsentMessage("aa", SourceKindPhrase, "bb", iat)
	assert.Equal(t, "freighter-user-link-v2\nsource\naa\nphrase\nbb\n1700000000", string(got))
	// Exactly six newline-delimited fields: the v1 five-field (no kind) message is retired.
	assert.Len(t, strings.Split(string(got), "\n"), 6)
}

func TestVerifyLinkConsent(t *testing.T) {
	iat := time.Unix(1700000000, 0)
	id, priv := newSource(t)
	signer, _ := newSource(t)
	other, otherPriv := newSource(t)

	cases := []struct {
		name            string
		id, kind, sub   string
		iat             time.Time
		sig             string
		wantErrContains string
	}{
		{name: "valid phrase consent", id: id, kind: SourceKindPhrase, sub: signer, iat: iat, sig: signConsent(id, SourceKindPhrase, signer, iat, priv)},
		{name: "valid secret_key consent", id: id, kind: SourceKindSecretKey, sub: signer, iat: iat, sig: signConsent(id, SourceKindSecretKey, signer, iat, priv)},
		{name: "signer is also the subject", id: id, kind: SourceKindPhrase, sub: id, iat: iat, sig: signConsent(id, SourceKindPhrase, id, iat, priv)},
		{name: "kind changed after signing", id: id, kind: SourceKindSecretKey, sub: signer, iat: iat, sig: signConsent(id, SourceKindPhrase, signer, iat, priv), wantErrContains: "does not verify"},
		{name: "signingSourceId changed after signing", id: id, kind: SourceKindPhrase, sub: other, iat: iat, sig: signConsent(id, SourceKindPhrase, signer, iat, priv), wantErrContains: "does not verify"},
		{name: "iat changed after signing", id: id, kind: SourceKindPhrase, sub: signer, iat: iat.Add(time.Second), sig: signConsent(id, SourceKindPhrase, signer, iat, priv), wantErrContains: "does not verify"},
		{name: "signed by another key", id: id, kind: SourceKindPhrase, sub: signer, iat: iat, sig: signConsent(id, SourceKindPhrase, signer, iat, otherPriv), wantErrContains: "does not verify"},
		{name: "unknown kind", id: id, kind: "hardware", sub: signer, iat: iat, sig: signConsent(id, "hardware", signer, iat, priv), wantErrContains: "unknown source kind"},
		{name: "signature not base64", id: id, kind: SourceKindPhrase, sub: signer, iat: iat, sig: "!!not-base64!!", wantErrContains: "not standard base64"},
		{name: "url-safe base64 is rejected", id: id, kind: SourceKindPhrase, sub: signer, iat: iat, sig: strings.NewReplacer("+", "-", "/", "_").Replace(signConsent(id, SourceKindPhrase, signer, iat, priv)) + "~", wantErrContains: "not standard base64"},
		{name: "signature wrong length", id: id, kind: SourceKindPhrase, sub: signer, iat: iat, sig: base64.StdEncoding.EncodeToString([]byte("short")), wantErrContains: "want 64"},
		{name: "uppercase id is not canonical", id: strings.ToUpper(id), kind: SourceKindPhrase, sub: signer, iat: iat, sig: signConsent(strings.ToUpper(id), SourceKindPhrase, signer, iat, priv), wantErrContains: "not a 32-byte lowercase hex"},
		{name: "id wrong length", id: id[:62], kind: SourceKindPhrase, sub: signer, iat: iat, sig: signConsent(id[:62], SourceKindPhrase, signer, iat, priv), wantErrContains: "not a 32-byte lowercase hex"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyLinkConsent(tc.id, tc.kind, tc.sub, tc.iat, tc.sig)
			if tc.wantErrContains == "" {
				assert.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrInvalidConsent)
			assert.Contains(t, err.Error(), tc.wantErrContains)
		})
	}
}

func TestIsCanonicalSourceID(t *testing.T) {
	id, _ := newSource(t)
	assert.True(t, IsCanonicalSourceID(id))
	assert.False(t, IsCanonicalSourceID(strings.ToUpper(id)))
	assert.False(t, IsCanonicalSourceID(id+"00"))
	assert.False(t, IsCanonicalSourceID(id[:63]+"g"))
	assert.False(t, IsCanonicalSourceID(""))
}
