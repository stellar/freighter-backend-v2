// ABOUTME: Internal package test for simulated_state_changes.go; keeps the deadcode analyser satisfied.
// ABOUTME: Calls the unexported isSimulatedStateChange() marker, which is never called at runtime.
package types

import "testing"

// TestSimulatedStateChangeBase_isSimulatedStateChange calls the sealed-interface
// marker through every variant so deadcode does not report it as unreachable.
func TestSimulatedStateChangeBase_isSimulatedStateChange(t *testing.T) {
	t.Parallel()
	variants := []SimulatedStateChange{
		SimulatedStateChangeBase{},
		&SimulatedBalanceChange{},
		&SimulatedAccountCreatedChange{},
		&SimulatedAccountMergedChange{},
		&SimulatedSignerAddedChange{},
		&SimulatedSignerUpdatedChange{},
		&SimulatedSignerRemovedChange{},
		&SimulatedThresholdChange{},
		&SimulatedAccountFlagsChange{},
		&SimulatedHomeDomainSetChange{},
		&SimulatedHomeDomainUpdatedChange{},
		&SimulatedHomeDomainClearedChange{},
		&SimulatedDataEntryAddedChange{},
		&SimulatedDataEntryUpdatedChange{},
		&SimulatedDataEntryRemovedChange{},
		&SimulatedAllowanceChange{},
		&SimulatedTrustlineAddedChange{},
		&SimulatedTrustlineUpdatedChange{},
		&SimulatedTrustlineRemovedChange{},
		&SimulatedBalanceAuthorizationChange{},
	}
	for _, v := range variants {
		v.isSimulatedStateChange()
	}
}
