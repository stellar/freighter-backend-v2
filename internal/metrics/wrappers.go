// ABOUTME: Decorator types that wrap RPCService and WalletBackendService with Prometheus instrumentation.
// ABOUTME: Uses a shared record() helper to avoid duplicating metrics recording logic across methods.
package metrics

import (
	"context"
	"time"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// Compile-time interface checks.
var (
	_ types.RPCService           = (*InstrumentedRPCService)(nil)
	_ types.WalletBackendService = (*InstrumentedWalletBackendService)(nil)
)

// record is a shared helper that records call metrics after a service call.
func record(m *Service, service, method, network string, duration float64, err error) {
	m.CallsTotal.WithLabelValues(service, method, network).Inc()
	m.CallDuration.WithLabelValues(service, method, network).Observe(duration)
	if err != nil {
		m.ErrorsTotal.WithLabelValues(service, method, network, ClassifyError(err)).Inc()
	}
}

// InstrumentedRPCService is a decorator that implements types.RPCService by
// delegating to an inner service and recording Prometheus metrics (call count,
// duration, errors) around each call. This keeps observability concerns out of
// the service internals and lets us swap instrumentation in or out at wiring time.
type InstrumentedRPCService struct {
	inner   types.RPCService
	metrics *Service
}

// NewInstrumentedRPCService creates a new instrumented RPC service decorator.
func NewInstrumentedRPCService(inner types.RPCService, m *Service) *InstrumentedRPCService {
	return &InstrumentedRPCService{inner: inner, metrics: m}
}

// Name delegates to the inner service.
func (i *InstrumentedRPCService) Name() string {
	return i.inner.Name()
}

// GetHealth records metrics around the inner GetHealth call.
func (i *InstrumentedRPCService) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	start := time.Now()
	result, err := i.inner.GetHealth(ctx, network)
	record(i.metrics, "rpc", "GetHealth", network, time.Since(start).Seconds(), err)
	return result, err
}

// SimulateTx records metrics around the inner SimulateTx call.
func (i *InstrumentedRPCService) SimulateTx(ctx context.Context, tx *txnbuild.Transaction, network string) (types.SimulateTransactionResponse, error) {
	start := time.Now()
	result, err := i.inner.SimulateTx(ctx, tx, network)
	record(i.metrics, "rpc", "SimulateTx", network, time.Since(start).Seconds(), err)
	return result, err
}

// SimulateInvocation records metrics around the inner SimulateInvocation call.
func (i *InstrumentedRPCService) SimulateInvocation(
	ctx context.Context,
	contractID xdr.ScAddress,
	sourceAccount *txnbuild.SimpleAccount,
	functionName xdr.ScSymbol,
	params []xdr.ScVal,
	timeout txnbuild.TimeBounds,
	network string,
) (types.SimulateTransactionResponse, error) {
	start := time.Now()
	result, err := i.inner.SimulateInvocation(ctx, contractID, sourceAccount, functionName, params, timeout, network)
	record(i.metrics, "rpc", "SimulateInvocation", network, time.Since(start).Seconds(), err)
	return result, err
}

// GetLedgerEntries records metrics around the inner GetLedgerEntries call.
func (i *InstrumentedRPCService) GetLedgerEntries(ctx context.Context, keys []string, network string) ([]types.LedgerEntryMap, error) {
	start := time.Now()
	result, err := i.inner.GetLedgerEntries(ctx, keys, network)
	record(i.metrics, "rpc", "GetLedgerEntries", network, time.Since(start).Seconds(), err)
	return result, err
}

// InstrumentedWalletBackendService is a decorator that implements types.WalletBackendService
// by delegating to an inner service and recording Prometheus metrics (call count,
// duration, errors) around each call. Same rationale as InstrumentedRPCService.
type InstrumentedWalletBackendService struct {
	inner   types.WalletBackendService
	metrics *Service
}

// NewInstrumentedWalletBackendService creates a new instrumented wallet backend service decorator.
func NewInstrumentedWalletBackendService(inner types.WalletBackendService, m *Service) *InstrumentedWalletBackendService {
	return &InstrumentedWalletBackendService{inner: inner, metrics: m}
}

// Name delegates to the inner service.
func (i *InstrumentedWalletBackendService) Name() string {
	return i.inner.Name()
}

// GetHealth records metrics around the inner GetHealth call.
func (i *InstrumentedWalletBackendService) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	start := time.Now()
	result, err := i.inner.GetHealth(ctx, network)
	record(i.metrics, "wallet-backend", "GetHealth", network, time.Since(start).Seconds(), err)
	return result, err
}

// GetBalancesByAccountAddresses records metrics around the inner GetBalancesByAccountAddresses call.
func (i *InstrumentedWalletBackendService) GetBalancesByAccountAddresses(ctx context.Context, addresses []string, network string) (interface{}, error) {
	start := time.Now()
	result, err := i.inner.GetBalancesByAccountAddresses(ctx, addresses, network)
	record(i.metrics, "wallet-backend", "GetBalancesByAccountAddresses", network, time.Since(start).Seconds(), err)
	return result, err
}
