package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/tools/stellar-hd-wallet/crypto/derivation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// linkProofVectorsPath is the fixture the extension and mobile clients pin
// (#166). It lives next to the verifier it proves rather than under testdata/
// because it is published for other repos, not only consumed by this test.
const linkProofVectorsPath = "link_proof_vectors.json"

// Source-identity derivation the clients ship, mirrored here so the vectors
// are proven to start from the material the clients already pin in
// freighter/@shared/api/helpers/authKeypairVectors.ts (and its mobile twin).
// The phrase values are copied verbatim from that file; the account addresses
// are the SEP-0005 vectors for the same mnemonic.
const (
	sourceIdentitySalt = "freighter-auth-v1"

	mnemonicAbandon   = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	abandonAuthSeed   = "cf8ef34afb730ffd0807ee8731f2378a4f6c702e2f14915976fac4afa711b52d"
	abandonPhraseID   = "ec57e5d04783b5ade776621ab171b3b197c3acc0a1cb5bad10786dc8e381e797"
	abandonAccount0   = "GB3JDWCQJCWMJ3IILWIGDTQJJC5567PGVEVXSCVPEQOTDN64VJBDQBYX"
	abandonAccount1   = "GDVSYYTUAJ3ACHTPQNSTQBDQ4LDHQCMNY4FCEQH5TJUMSSLWQSTG42MV"
	mnemonicIllness   = "illness spike retreat truth genius clock brain pass fit cave bargain toe"
	illnessAuthSeed   = "882835b30a2c5b011f1b2424a84f4cd39f342f4d12be574e4233dbf9b98976d1"
	illnessPhraseID   = "bd9498475c7191c5e9a5e18edda2402ab0ae527580a6c38b2a32a77c65729cd7"
	stellarAccountFmt = "m/44'/148'/%d'"
)

type linkProofVector struct {
	Name            string `json:"name"`
	Seed            string `json:"seed"`
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	SigningSourceID string `json:"signingSourceId"`
	IAT             int64  `json:"iat"`
	Message         string `json:"message"`
	Sig             string `json:"sig"`
	ExpectVerify    bool   `json:"expectVerify"`
}

type linkProofVectorFile struct {
	Description string            `json:"description"`
	Issue       string            `json:"issue"`
	Domain      string            `json:"domain"`
	Vectors     []linkProofVector `json:"vectors"`
}

func loadLinkProofVectors(t *testing.T) linkProofVectorFile {
	t.Helper()
	raw, err := os.ReadFile(linkProofVectorsPath)
	require.NoError(t, err)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields() // the schema is a contract: no stray fields
	var file linkProofVectorFile
	require.NoError(t, dec.Decode(&file))
	require.NotEmpty(t, file.Vectors)
	return file
}

// bip39Seed is the 64-byte BIP39 seed of an English mnemonic with an empty
// passphrase, what bip39.mnemonicToSeedSync returns in the clients.
func bip39Seed(t *testing.T, mnemonic string) []byte {
	t.Helper()
	seed, err := pbkdf2.Key(sha512.New, mnemonic, []byte("mnemonic"), 2048, 64)
	require.NoError(t, err)
	return seed
}

// sourceSeed is the 32-byte ed25519 seed of a source identity:
// HMAC-SHA256 keyed on the input material over the salt, the orientation
// deriveAuthSeed uses in the extension (importKey(seedBytes); sign(AUTH_SALT)).
func sourceSeed(material []byte) []byte {
	mac := hmac.New(sha256.New, material)
	mac.Write([]byte(sourceIdentitySalt))
	return mac.Sum(nil)
}

// accountRawSeed is the raw 32-byte secret of account `index` of a mnemonic
// (SLIP-0010 at m/44'/148'/index'), what Keypair.rawSecretKey() returns for
// a derived or exported account key.
func accountRawSeed(t *testing.T, seed64 []byte, index int) []byte {
	t.Helper()
	key, err := derivation.DeriveForPath(fmt.Sprintf(stellarAccountFmt, index), seed64)
	require.NoError(t, err)
	raw := key.RawSeed()
	return raw[:]
}

func vectorKey(t *testing.T, v linkProofVector) ed25519.PrivateKey {
	t.Helper()
	seed, err := hex.DecodeString(v.Seed)
	require.NoError(t, err, "seed is not hex")
	require.Len(t, seed, ed25519.SeedSize, "seed is not 32 bytes")
	return ed25519.NewKeyFromSeed(seed)
}

func TestLinkProofVectors_Header(t *testing.T) {
	file := loadLinkProofVectors(t)
	assert.Contains(t, file.Issue, "github.com/stellar/freighter-backend-v2/issues/166")
	assert.Equal(t, LinkConsentDomain, file.Domain)
	assert.NotEmpty(t, file.Description)
}

func TestLinkProofVectors_EachVector(t *testing.T) {
	file := loadLinkProofVectors(t)
	seen := map[string]bool{}
	for _, v := range file.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			require.NotEmpty(t, v.Name)
			require.False(t, seen[v.Name], "duplicate vector name")
			seen[v.Name] = true

			priv := vectorKey(t, v)
			assert.Equal(t, v.ID, hex.EncodeToString(priv.Public().(ed25519.PublicKey)), "id is not the public key of seed")
			assert.True(t, IsCanonicalSourceID(v.ID))
			assert.True(t, IsValidSourceKind(v.Kind))
			assert.True(t, IsCanonicalSourceID(v.SigningSourceID))

			iat := time.Unix(v.IAT, 0)
			assert.Equal(t, v.Message, string(LinkConsentMessage(v.ID, v.Kind, v.SigningSourceID, iat)), "message is not what the fields build")

			err := VerifyLinkConsent(v.ID, v.Kind, v.SigningSourceID, iat, v.Sig)
			if !v.ExpectVerify {
				require.ErrorIs(t, err, ErrInvalidConsent)
				return
			}
			require.NoError(t, err)
			// ed25519 is deterministic, so re-signing must reproduce sig byte for byte.
			resigned := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(v.Message)))
			assert.Equal(t, v.Sig, resigned, "re-signing from seed does not reproduce sig")
		})
	}
}

func TestLinkProofVectors_Coverage(t *testing.T) {
	file := loadLinkProofVectors(t)

	var phrase, secretKey, selfSigned int
	positiveBySig := map[string]linkProofVector{}
	for _, v := range file.Vectors {
		if !v.ExpectVerify {
			continue
		}
		positiveBySig[v.Sig] = v
		switch v.Kind {
		case SourceKindPhrase:
			phrase++
		case SourceKindSecretKey:
			secretKey++
		}
		if v.ID == v.SigningSourceID {
			selfSigned++
		}
	}
	assert.GreaterOrEqual(t, phrase, 1, "want a positive phrase consent")
	assert.GreaterOrEqual(t, secretKey, 1, "want a positive secret_key consent")
	assert.GreaterOrEqual(t, selfSigned, 1, "want a consent whose signer is also the subject")

	// Every negative vector is a positive vector with exactly one field
	// altered after signing: either kind or signingSourceId. Both kinds of
	// alteration must be present.
	var kindAltered, signerAltered int
	for _, v := range file.Vectors {
		if v.ExpectVerify {
			continue
		}
		orig, ok := positiveBySig[v.Sig]
		require.True(t, ok, "%s: negative vector's sig does not belong to a positive vector", v.Name)
		assert.Equal(t, orig.Seed, v.Seed, "%s: seed differs from the signed vector", v.Name)
		assert.Equal(t, orig.ID, v.ID, "%s: id differs from the signed vector", v.Name)
		assert.Equal(t, orig.IAT, v.IAT, "%s: iat differs from the signed vector", v.Name)
		switch {
		case orig.Kind != v.Kind && orig.SigningSourceID == v.SigningSourceID:
			kindAltered++
		case orig.Kind == v.Kind && orig.SigningSourceID != v.SigningSourceID:
			signerAltered++
		default:
			t.Errorf("%s: want exactly one of kind or signingSourceId altered", v.Name)
		}
	}
	assert.GreaterOrEqual(t, kindAltered, 1, "want a negative vector with kind altered")
	assert.GreaterOrEqual(t, signerAltered, 1, "want a negative vector with signingSourceId altered")
}

// TestLinkProofVectors_DerivationTie proves the vectors start from the
// material the clients already pin: the phrase vectors' seeds are the
// clients' authSeedHex values, and the secret_key vectors' seeds come from
// the SEP-0005 account keys of the same mnemonic. A client that passes its
// derivation tests and these vectors has the whole path from mnemonic to
// consent signature pinned.
func TestLinkProofVectors_DerivationTie(t *testing.T) {
	file := loadLinkProofVectors(t)

	positive := func(kind, seedHex string) *linkProofVector {
		for i := range file.Vectors {
			v := &file.Vectors[i]
			if v.ExpectVerify && v.Kind == kind && v.Seed == seedHex {
				return v
			}
		}
		return nil
	}

	// Phrase sources: HMAC over the 64-byte BIP39 seed, the shipped derivation.
	abandon64 := bip39Seed(t, mnemonicAbandon)
	require.Equal(t, abandonAuthSeed, hex.EncodeToString(sourceSeed(abandon64)), "derivation diverged from authKeypairVectors.ts")
	v := positive(SourceKindPhrase, abandonAuthSeed)
	require.NotNil(t, v, "want a phrase vector derived from the abandon mnemonic")
	assert.Equal(t, abandonPhraseID, v.ID)

	illness64 := bip39Seed(t, mnemonicIllness)
	require.Equal(t, illnessAuthSeed, hex.EncodeToString(sourceSeed(illness64)), "derivation diverged from authKeypairVectors.ts")
	v = positive(SourceKindPhrase, illnessAuthSeed)
	require.NotNil(t, v, "want a phrase vector derived from the illness mnemonic")
	assert.Equal(t, illnessPhraseID, v.ID)

	// Secret-key sources: HMAC over the account's raw 32-byte secret.
	for i, wantAddr := range []string{abandonAccount0, abandonAccount1} {
		raw := accountRawSeed(t, abandon64, i)
		kp, err := keypair.FromRawSeed([32]byte(raw))
		require.NoError(t, err)
		require.Equal(t, wantAddr, kp.Address(), "account %d diverged from the SEP-0005 vectors", i)
		v = positive(SourceKindSecretKey, hex.EncodeToString(sourceSeed(raw)))
		assert.NotNil(t, v, "want a secret_key vector derived from abandon account %d", i)
	}
}
