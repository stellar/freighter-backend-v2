package handlers

import (
	"net/http"

	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/auth"
)

// WhoamiHandler echoes the authenticated user ID derived from the request's JWT.
// It is a thin surface for exercising the auth middleware end-to-end and for
// client teams to validate their JWT construction against a deployed environment.
type WhoamiHandler struct{}

// WhoamiResponse reports whether the request was authenticated and, if so, the
// user ID (hex-encoded auth public key) the token was signed for.
type WhoamiResponse struct {
	Authenticated bool   `json:"authenticated"`
	UserID        string `json:"userId,omitempty"`
}

func NewWhoamiHandler() *WhoamiHandler {
	return &WhoamiHandler{}
}

func (h *WhoamiHandler) Whoami(w http.ResponseWriter, r *http.Request) error {
	userID, ok := auth.UserIDFromContext(r.Context())

	w.Header().Set("Content-Type", "application/json")
	return response.OK(w, HttpResponse{Data: WhoamiResponse{Authenticated: ok, UserID: userID}})
}
