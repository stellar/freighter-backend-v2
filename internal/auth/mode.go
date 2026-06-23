package auth

import "fmt"

// Mode controls how the auth middleware treats requests for a route.
type Mode int

const (
	// Permissive allows requests with no bearer token through anonymously, but
	// still rejects a present-but-invalid token with 401. Used during the client
	// rollout, before all clients send JWTs.
	Permissive Mode = iota
	// Required rejects any request that is not accompanied by a valid token.
	Required
)

func (m Mode) String() string {
	switch m {
	case Permissive:
		return "permissive"
	case Required:
		return "strict"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// ParseMode maps a config string to a Mode. "permissive" and "strict" are the
// only accepted values. Any other value — including the empty string — is an
// error: the permissive default lives solely in the --auth-mode flag definition,
// so an unset value here means the config was never populated (a real bug) rather
// than a silent fail-open.
func ParseMode(s string) (Mode, error) {
	switch s {
	case "permissive":
		return Permissive, nil
	case "strict":
		return Required, nil
	default:
		return 0, fmt.Errorf("invalid auth mode %q (want \"permissive\" or \"strict\")", s)
	}
}
