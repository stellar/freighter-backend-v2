package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/users"
)

// SourceLinker is the users.Linker surface the handler depends on, narrowed to
// an interface so the request-validation and rejection paths can be tested
// against a fake that fails the test if it is ever reached.
type SourceLinker interface {
	Link(ctx context.Context, signerID string, sources []users.Source) (*users.LinkResult, error)
}

// LinkRequestTimeout is the one budget for a link request's database work:
// pool acquisition and the whole transaction share it. Nothing else bounds
// that work. The request context has no deadline, http.Server.WriteTimeout
// only fails the response write and does not cancel the handler, and the
// store's SET LOCAL timeouts start after a connection is acquired and apply
// per statement. It stays under api.DefaultWriteTimeout (10s) with room to
// write the response; serve_test.go guards that relation, since this package
// cannot import the server's constant.
const LinkRequestTimeout = 8 * time.Second

// linkRetryAfterSeconds is the Retry-After the handler sends with a 503 for a
// signer-lock timeout. The lock is held for one transaction, normally
// milliseconds, so one second is ample for the holder to finish.
const linkRetryAfterSeconds = "1"

// UserLinkHandler serves POST /api/v1/user/link, the only write to the identity
// tables. It is the one place a source becomes part of a user.
//
// Trust model: a valid JWT proves possession of some ed25519 keypair, and anyone
// can mint one, so the body carries no claimed source or user id that the server
// would act on. Every source in the body carries a consent signed by that
// source's own key over the v2 consent message (auth.LinkConsentMessage), which
// binds it to the caller's JWT `sub` and the token's verified `iat`. The handler
// verifies all of them before anything is written and rejects the whole request
// on any failure: linking must be proven, not asserted.
//
// The route must be wrapped with auth.Required regardless of the global
// AUTH_MODE. Production runs permissive, where an anonymous request falls
// through with no source id; this handler refuses such a request with 401 as a
// backstop, but the wiring (serve.go routes()) is what guarantees it.
type UserLinkHandler struct {
	linker SourceLinker
	// metrics may be nil (tests); every record call is then a no-op.
	metrics *metrics.UserLink
}

// NewUserLinkHandler returns a handler that writes through linker. Pass a true
// nil interface when the database is disabled: the route then answers 503
// rather than panicking, matching db-health's treatment of a missing pool.
func NewUserLinkHandler(linker SourceLinker, m *metrics.UserLink) *UserLinkHandler {
	return &UserLinkHandler{linker: linker, metrics: m}
}

// UserLinkRequest is the request body. Only `sources` is read; any other field
// is ignored. In particular the signing source and the issue time the consents
// are bound to come from the verified JWT, never from the body.
type UserLinkRequest struct {
	Sources []UserLinkSource `json:"sources"`
}

// UserLinkSource is one consent: the source's hex public key, its kind, and a
// standard-base64 ed25519 signature by that key over the consent message.
// Kind is part of the signed bytes, so a kind edited after signing fails
// verification rather than being stored.
type UserLinkSource struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Sig  string `json:"sig"`
}

// UserLinkResponse reports the caller's user id (the cluster's canonical source
// id), every source now in the caller's cluster, and the submitted ids that
// belong to another user and were left untouched.
type UserLinkResponse struct {
	UserID    string             `json:"userId"`
	Sources   []UserLinkedSource `json:"sources"`
	Conflicts []string           `json:"conflicts"`
}

// UserLinkedSource is one row of the caller's cluster.
type UserLinkedSource struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
}

// Link handles POST /api/v1/user/link.
//
// Status codes:
//   - 400: malformed JSON, trailing data after the JSON object, no sources,
//     more than users.MaxLinkSources, a repeated id, an id that is not a
//     canonical hex public key, an unknown kind, or a body that does not
//     include the JWT `sub`.
//   - 401: no authenticated source on the request (should be unreachable
//     behind auth.Required).
//   - 403: a consent that does not verify: forged, signed by another key, or
//     bound to a different signer, kind, or issue time than this request's.
//   - 503: the database is disabled, the request was canceled, or (with a
//     Retry-After header) the store timed out waiting for the signer lock
//     behind another link signed as the same source.
//   - 504: LinkRequestTimeout passed (pool acquisition plus the whole
//     transaction), or one of the store's own statements timed out.
func (h *UserLinkHandler) Link(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	sub, ok := auth.SourceIDFromContext(ctx)
	if !ok {
		return httperror.Unauthorized("unauthorized", errors.New("link requires an authenticated source"))
	}
	iat, ok := auth.IssuedAtFromContext(ctx)
	if !ok {
		// The middleware sets iat exactly when it sets the source id, so this is a
		// wiring bug, not a client error.
		metrics.RecordUserLink(h.metrics, metrics.LinkResultError)
		return httperror.InternalServerError("An unexpected error occurred", errors.New("authenticated request has no verified iat"))
	}

	var req UserLinkRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		metrics.RecordUserLink(h.metrics, metrics.LinkResultBadRequest)
		return httperror.BadRequest("invalid JSON", err)
	}
	// Decode reads one JSON value and ignores the rest of the body. The body is
	// the whole request, so anything after the object (garbage or a second
	// value) is malformed; only whitespace, which leaves the decoder at EOF, is
	// allowed.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("a second JSON value follows the request object")
		}
		metrics.RecordUserLink(h.metrics, metrics.LinkResultBadRequest)
		return httperror.BadRequest("invalid JSON: trailing data after the request object", err)
	}

	sources, err := validateLinkSources(req.Sources, sub, iat)
	if err != nil {
		metrics.RecordUserLink(h.metrics, linkResultForStatus(err))
		return err
	}

	if h.linker == nil {
		metrics.RecordUserLink(h.metrics, metrics.LinkResultError)
		return httperror.ServiceUnavailable("Database is disabled", nil)
	}

	linkCtx, cancel := context.WithTimeout(ctx, LinkRequestTimeout)
	defer cancel()
	res, err := h.linker.Link(linkCtx, sub, sources)
	if err != nil {
		switch {
		case errors.Is(err, users.ErrTooManySources),
			errors.Is(err, users.ErrRepeatedSource),
			errors.Is(err, users.ErrSignerNotInBody),
			errors.Is(err, users.ErrUnknownSourceKind),
			errors.Is(err, users.ErrNonCanonicalSourceID):
			// validateLinkSources already enforces these; reaching here means the
			// two drifted. Still a client-visible 400, but log it as the bug it is.
			logger.ErrorWithContext(ctx, "link store rejected a request the handler accepted", "error", err)
			metrics.RecordUserLink(h.metrics, metrics.LinkResultBadRequest)
			return httperror.BadRequest("invalid link request", err)
		case errors.Is(err, users.ErrLockTimeout):
			// Another link signed as the same source held the signer lock past the
			// store's lock timeout. Transient and expected under same-signer
			// concurrency, so it is a retryable 503 at warn level, not a 500 that
			// pages for a bug.
			logger.WarnWithContext(ctx, "link timed out waiting for the signer lock", "error", err)
			metrics.RecordUserLink(h.metrics, metrics.LinkResultContended)
			w.Header().Set("Retry-After", linkRetryAfterSeconds)
			return httperror.ServiceUnavailable("another link for this signer is in progress; retry shortly", err)
		case errors.Is(err, users.ErrStatementTimeout), errors.Is(err, context.DeadlineExceeded):
			// Either the store's own statement timeout or LinkRequestTimeout (which
			// also covers waiting for a pool connection). Same mapping as the
			// wallet-backend handlers: a timeout is a 504, not an operational 500.
			logger.WarnWithContext(ctx, "linking sources timed out", "error", err)
			metrics.RecordUserLink(h.metrics, metrics.LinkResultError)
			return httperror.GatewayTimeout("linking sources timed out", err)
		case errors.Is(err, context.Canceled):
			metrics.RecordUserLink(h.metrics, metrics.LinkResultError)
			return httperror.ServiceUnavailable("request canceled", err)
		default:
			logger.ErrorWithContext(ctx, "linking sources failed", "error", err)
			metrics.RecordUserLink(h.metrics, metrics.LinkResultError)
			return httperror.InternalServerError("An unexpected error occurred", err)
		}
	}

	if res.Created {
		metrics.RecordUserLink(h.metrics, metrics.LinkResultCreated)
	} else {
		metrics.RecordUserLink(h.metrics, metrics.LinkResultResolved)
	}
	metrics.RecordUserLinkSources(h.metrics, metrics.LinkSourceWritten, res.Written)
	metrics.RecordUserLinkSources(h.metrics, metrics.LinkSourceConflict, len(res.Conflicts))

	out := UserLinkResponse{
		UserID:    res.CanonicalSourceID,
		Sources:   make([]UserLinkedSource, 0, len(res.Sources)),
		Conflicts: res.Conflicts,
	}
	for _, s := range res.Sources {
		out.Sources = append(out.Sources, UserLinkedSource{ID: s.ID, Kind: s.Kind, CreatedAt: s.CreatedAt})
	}

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: out})
}

// linkResultForStatus maps a validation error to its metric label: 403 is a
// consent that did not verify, everything else validateLinkSources returns is
// a 400.
func linkResultForStatus(err error) string {
	var he *httperror.HttpError
	if errors.As(err, &he) && he.StatusCode == http.StatusForbidden {
		return metrics.LinkResultBadConsent
	}
	return metrics.LinkResultBadRequest
}

// validateLinkSources applies every structural check, then verifies every
// consent, and returns the verified sources with their kinds taken from the
// signed bytes. It runs before any database work so a rejected request writes
// nothing. Structural failures are 400; a consent that does not verify is 403.
//
// The structural checks run first so a request that is malformed on its face
// is rejected without spending an ed25519 verify per entry.
func validateLinkSources(in []UserLinkSource, sub string, iat time.Time) ([]users.Source, error) {
	if len(in) == 0 {
		return nil, httperror.BadRequest("sources must not be empty", nil)
	}
	if len(in) > users.MaxLinkSources {
		return nil, httperror.BadRequestf("too many sources: %d (max %d)", len(in), users.MaxLinkSources)
	}

	seen := make(map[string]struct{}, len(in))
	signerPresent := false
	for i, s := range in {
		if !auth.IsCanonicalSourceID(s.ID) {
			return nil, httperror.BadRequestf("sources[%d].id must be a 64-character lowercase hex public key", i)
		}
		if _, dup := seen[s.ID]; dup {
			return nil, httperror.BadRequestf("sources[%d].id is repeated", i)
		}
		seen[s.ID] = struct{}{}
		if !auth.IsValidSourceKind(s.Kind) {
			return nil, httperror.BadRequestf("sources[%d].kind %q is not one of %q, %q", i, s.Kind, auth.SourceKindPhrase, auth.SourceKindSecretKey)
		}
		if s.ID == sub {
			signerPresent = true
		}
	}
	if !signerPresent {
		return nil, httperror.BadRequest("sources must include the authenticated source (the JWT sub)", nil)
	}

	out := make([]users.Source, 0, len(in))
	for i, s := range in {
		if err := auth.VerifyLinkConsent(s.ID, s.Kind, sub, iat, s.Sig); err != nil {
			// The detail names the index, not the signature or key, so a rejected
			// consent is diagnosable without echoing client-controlled bytes.
			return nil, httperror.Forbidden(fmt.Sprintf("sources[%d]: consent does not verify", i), err)
		}
		out = append(out, users.Source{ID: s.ID, Kind: s.Kind})
	}
	return out, nil
}
