package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/auth"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/users"
)

// UserReader is the users.Store surface the handler depends on, narrowed to an
// interface so the unit tests can fake it and count calls: the handler makes
// exactly one ResolveUser call and one ListSources call per request.
type UserReader interface {
	ResolveUser(ctx context.Context, sourceID string) (userID uuid.UUID, canonicalSourceID string, err error)
	ListSources(ctx context.Context, userID uuid.UUID) ([]users.LinkedSource, error)
}

// UserRequestTimeout is the budget for the handler's two reads, pool
// acquisition included. It exists for the same reason LinkRequestTimeout does:
// the request context has no deadline and http.Server.WriteTimeout only fails
// the response write, it does not cancel the handler. Unlike link, these reads
// run outside a transaction and so under no SET LOCAL statement_timeout; this
// is their only bound. It stays under api.DefaultWriteTimeout (10s);
// serve_test.go guards that relation.
const UserRequestTimeout = 5 * time.Second

// UserHandler serves GET /api/v1/user, the client's one source of truth for
// its identity: the canonical source id of the cluster the caller's source
// belongs to, and every source in that cluster. The JWT `sub` is a SOURCE id,
// so echoing it (as whoami does) is wrong for any wallet whose pinned source is
// not the cluster root; this handler resolves it.
//
// The two reads run as separate statements on the pool, not in a transaction.
// That is safe because the identity tables are append-only: no user_sources
// row is ever deleted or moved, no users row is deleted, and
// canonical_source_id never changes. Everything ResolveUser returns is
// therefore permanently true once observed, and every row ListSources returns
// belongs to that user for good. The only interleaving is a concurrent link
// adding a row between the two statements, which makes the response a
// slightly newer snapshot. If detach or merge is ever implemented, this
// handler must move onto one snapshot.
//
// The route must be wrapped with auth.Required regardless of the global
// AUTH_MODE: there is no anonymous form of "who am I". The handler refuses an
// anonymous request with 401 as a backstop, but the wiring (serve.go routes())
// is what guarantees it.
//
// There is no dedicated metric family. The HTTP middleware meters the route by
// status, and its 404 rate is the "authenticated but not yet linked" signal.
type UserHandler struct {
	store UserReader
}

// NewUserHandler returns a handler that reads through store. Pass a true nil
// interface when the database is disabled: the route then answers 503 rather
// than panicking, matching the link handler's treatment of a missing pool.
func NewUserHandler(store UserReader) *UserHandler {
	return &UserHandler{store: store}
}

// UserResponse is the client contract: the caller's user id (the cluster's
// canonical source id) and every source in the cluster. It carries no G
// address: the identity tables hold none, and every id is a hex ed25519 key.
type UserResponse struct {
	UserID  string             `json:"userId"`
	Sources []UserLinkedSource `json:"sources"`
}

// GetUser handles GET /api/v1/user.
//
// Status codes:
//   - 401: no authenticated source on the request (should be unreachable
//     behind auth.Required).
//   - 404: the source has never been linked. The client treats this as "not
//     yet linked" and uses its pinned source id locally.
//   - 503: the database is disabled, or the request was canceled.
//   - 504: UserRequestTimeout passed.
func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	sub, ok := auth.SourceIDFromContext(ctx)
	if !ok {
		return httperror.Unauthorized("unauthorized", errors.New("user lookup requires an authenticated source"))
	}
	if h.store == nil {
		return httperror.ServiceUnavailable("Database is disabled", nil)
	}

	readCtx, cancel := context.WithTimeout(ctx, UserRequestTimeout)
	defer cancel()

	userID, canonical, err := h.store.ResolveUser(readCtx, sub)
	if err != nil {
		return mapUserStoreErr(ctx, err, "resolving user")
	}
	sources, err := h.store.ListSources(readCtx, userID)
	if err != nil {
		return mapUserStoreErr(ctx, err, "reading user sources")
	}
	if len(sources) == 0 {
		// The resolved source's own row is under this user and rows are never
		// deleted, so an empty cluster is an invariant violation, not a 404.
		err = errors.New("resolved user has no sources")
		logger.ErrorWithContext(ctx, "reading user sources returned an empty cluster", "error", err)
		return httperror.InternalServerError("An unexpected error occurred", err)
	}

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: UserResponse{
		UserID:  canonical,
		Sources: toUserLinkedSources(sources),
	}})
}

// mapUserStoreErr maps a store error to its status. Not-found is checked
// first: it is the expected answer for an unlinked wallet and is not logged.
// The message never echoes the source id. what names the step for the log.
func mapUserStoreErr(ctx context.Context, err error, what string) error {
	switch {
	case errors.Is(err, users.ErrSourceNotFound):
		return httperror.NotFound("source is not linked to any user", err)
	case errors.Is(err, context.DeadlineExceeded):
		logger.WarnWithContext(ctx, what+" timed out", "error", err)
		return httperror.GatewayTimeout(what+" timed out", err)
	case errors.Is(err, context.Canceled):
		return httperror.ServiceUnavailable("request canceled", err)
	default:
		logger.ErrorWithContext(ctx, what+" failed", "error", err)
		return httperror.InternalServerError("An unexpected error occurred", err)
	}
}

// toUserLinkedSources converts store rows to the wire shape shared by
// GET /api/v1/user and POST /api/v1/user/link. Never nil, so `sources`
// marshals as [] rather than null.
func toUserLinkedSources(in []users.LinkedSource) []UserLinkedSource {
	out := make([]UserLinkedSource, 0, len(in))
	for _, s := range in {
		out = append(out, UserLinkedSource{ID: s.ID, Kind: s.Kind, CreatedAt: s.CreatedAt})
	}
	return out
}
