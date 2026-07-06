// ABOUTME: Internal package tests for account_balances.go; keeps the deadcode analyser satisfied.
// ABOUTME: The isBalance() marker method is unexported and never called at runtime, so we call
// it here to prevent golang.org/x/tools/cmd/deadcode from flagging it as unreachable.
package types

import "testing"

// TestBalanceBase_isBalance exercises the sealed-interface marker method so the
// deadcode analyser (which uses RTA and only marks a method reachable when it finds a
// call site on an interface value) does not report BalanceBase.isBalance as dead.
func TestBalanceBase_isBalance(t *testing.T) {
	t.Parallel()
	// Call the marker through each concrete type so RTA sees the dynamic dispatch.
	variants := []Balance{
		BalanceBase{},
		&NativeBalance{},
		&TrustlineBalance{},
		&SACBalance{},
		&SEP41Balance{},
		&LiquidityPoolBalance{},
	}
	for _, v := range variants {
		v.isBalance() // marker call — keeps deadcode happy
	}
}
