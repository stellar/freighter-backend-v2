package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LinkConsentDomain is the domain-separation prefix of the v2 user-link consent
// message. It is the first line of every consent a client signs for
// POST /api/v1/user/link, so a consent can never be confused with an auth JWT
// or any other message signed by the same key.
//
// The format is a cross-platform contract pinned by fixture vectors
// (internal/auth/link_proof_vectors.json, #166) that the extension and mobile
// clients replay in their own test suites. Changing the message in any way
// invalidates every client, so a later format gets a NEW prefix and a new vector
// file rather than an edit to this one. The retired three-field
// `freighter-user-link-v1` message (no kind) must not be reintroduced.
const LinkConsentDomain = "freighter-user-link-v2"

// Source kinds a user can prove possession of. The kind is part of the signed
// consent and is stored verbatim from the signed bytes, never from an unsigned
// field, so a client cannot relabel a source after signing.
const (
	SourceKindPhrase    = "phrase"     // a seed phrase (a user's root identity)
	SourceKindSecretKey = "secret_key" // a single account secret key
)

// ErrInvalidConsent is the sentinel every consent-verification failure wraps.
// Callers test it with errors.Is; the wrapped detail says which check failed.
var ErrInvalidConsent = errors.New("invalid link consent")

// IsValidSourceKind reports whether kind is one of the kinds the link endpoint
// accepts. Anything else is rejected before verification.
func IsValidSourceKind(kind string) bool {
	return kind == SourceKindPhrase || kind == SourceKindSecretKey
}

// LinkConsentMessage builds the exact bytes a source signs to consent to being
// linked: newline-delimited, domain-separated, with the issue time as unix
// seconds.
//
//	freighter-user-link-v2 \n source \n <id> \n <kind> \n <signingSourceId> \n <iat>
//
// id is the hex public key of the source giving consent, kind is its
// SourceKind, signingSourceID is the JWT `sub` of the caller the consent is
// bound to, and iat is that JWT's verified issued-at. Binding the consent to
// the signer and the token's issue time means a captured consent cannot be
// replayed by another caller or under a later token.
func LinkConsentMessage(id, kind, signingSourceID string, iat time.Time) []byte {
	return []byte(strings.Join([]string{
		LinkConsentDomain,
		"source",
		id,
		kind,
		signingSourceID,
		strconv.FormatInt(iat.Unix(), 10),
	}, "\n"))
}

// VerifyLinkConsent checks that sigB64 (standard base64) is a valid ed25519
// signature by the key in id (hex) over LinkConsentMessage(id, kind,
// signingSourceID, iat). Every failure wraps ErrInvalidConsent.
//
// The caller passes the kind it read from the request and the signingSourceID
// and iat it took from the verified JWT. Because all three are part of the
// signed bytes, a kind changed after signing, a consent signed for a different
// caller, or a consent minted under a different token all fail here as a plain
// signature mismatch: there is nothing unsigned to trust.
//
// id must already be the canonical 64-character lowercase hex form (see
// IsCanonicalSourceID): the bytes verified are the bytes stored, so the two
// must not be allowed to diverge through case.
func VerifyLinkConsent(id, kind, signingSourceID string, iat time.Time, sigB64 string) error {
	if !IsCanonicalSourceID(id) {
		return fmt.Errorf("%w: source id %q is not a 32-byte lowercase hex public key", ErrInvalidConsent, id)
	}
	pub, err := hex.DecodeString(id)
	if err != nil {
		// Unreachable after IsCanonicalSourceID, kept so a future relaxation of
		// that check cannot turn a decode failure into a panic below.
		return fmt.Errorf("%w: decoding source id: %w", ErrInvalidConsent, err)
	}
	if !IsValidSourceKind(kind) {
		return fmt.Errorf("%w: unknown source kind %q", ErrInvalidConsent, kind)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("%w: signature is not standard base64: %w", ErrInvalidConsent, err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature is %d bytes, want %d", ErrInvalidConsent, len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), LinkConsentMessage(id, kind, signingSourceID, iat), sig) {
		return fmt.Errorf("%w: signature does not verify for source %s", ErrInvalidConsent, id)
	}
	return nil
}

// IsCanonicalSourceID reports whether id is the canonical wire form of a source
// id: exactly 64 lowercase hex characters, i.e. a 32-byte ed25519 public key.
// The JWT parser lowercases `sub` before exposing it as the source id, and the
// link endpoint demands the same form in the body so the id a consent is signed
// over, the id compared against `sub`, and the id stored in user_sources are
// one and the same string.
func IsCanonicalSourceID(id string) bool {
	if len(id) != 2*ed25519.PublicKeySize {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
