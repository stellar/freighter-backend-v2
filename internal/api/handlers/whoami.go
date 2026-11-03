package handlers

import (
	"net/http"

	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/auth"
)

// WhoamiHandler echoes the authenticated source id derived from the request's
// JWT. It is a thin surface for exercising the auth middleware end-to-end and for
// client teams to validate their JWT construction against a deployed environment.
//
// It deliberately does NOT call users.ResolveUser: it reports what the signature
// proved and nothing else, so it needs no database and works for a key that has
// never been linked. The resolved view of the caller is GET /api/v1/user.
type WhoamiHandler struct{}

// WhoamiResponse reports whether the request was authenticated and, if so, the
// source id (hex-encoded auth public key) the token was signed with. The wire
// field keeps its original name `userId` so existing clients that read it keep
// working; for a source that has not been linked to a larger cluster it is also
// the user id as exposed.
type WhoamiResponse struct {
	Authenticated bool   `json:"authenticated"`
	UserID        string `json:"userId,omitempty"`
}

func NewWhoamiHandler() *WhoamiHandler {
	return &WhoamiHandler{}
}

func (h *WhoamiHandler) Whoami(w http.ResponseWriter, r *http.Request) error {
	sourceID, ok := auth.SourceIDFromContext(r.Context())

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: WhoamiResponse{Authenticated: ok, UserID: sourceID}})
}
