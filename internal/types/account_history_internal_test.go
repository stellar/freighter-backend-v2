// ABOUTME: Internal package tests for account_history.go; keeps the deadcode analyser satisfied.
// ABOUTME: The isStateChange() marker method is unexported and never called at runtime, so we call
// it here to prevent golang.org/x/tools/cmd/deadcode from flagging it as unreachable.
package types

import "testing"

// TestStateChangeBase_isStateChange exercises the sealed-interface marker method so the
// deadcode analyser (which uses RTA and only marks a method reachable when it finds a
// call site on an interface value) does not report StateChangeBase.isStateChange as dead.
func TestStateChangeBase_isStateChange(t *testing.T) {
	t.Parallel()
	// Call the marker through each concrete type so RTA sees the dynamic dispatch.
	variants := []StateChange{
		StateChangeBase{},
		&StandardBalanceChange{},
		&AccountChange{},
		&SignerChange{},
		&SignerThresholdsChange{},
		&MetadataChange{},
		&FlagsChange{},
		&TrustlineChange{},
		&ReservesChange{},
		&BalanceAuthorizationChange{},
	}
	for _, v := range variants {
		v.isStateChange() // marker call — keeps deadcode happy
	}
}
