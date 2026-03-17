// ABOUTME: Unit tests for instrumented service decorators.
// ABOUTME: Verifies metrics are recorded and inner results pass through unchanged for both RPC and WalletBackend services.
package metrics

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func newTestService(t *testing.T) (*Service, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	s := NewService(reg)
	return s, reg
}

func TestInstrumentedRPCService_GetHealth_RecordsMetrics(t *testing.T) {
	svc, _ := newTestService(t)
	mock := &utils.MockRPCService{}
	instrumented := NewInstrumentedRPCService(mock, svc)

	result, err := instrumented.GetHealth(context.Background(), types.TESTNET)
	require.NoError(t, err)
	assert.Equal(t, types.StatusHealthy, result.Status)

	expected := `
		# HELP freighter_service_calls_total Total number of external service calls.
		# TYPE freighter_service_calls_total counter
		freighter_service_calls_total{method="GetHealth",network="TESTNET",service="rpc"} 1
	`
	require.NoError(t, testutil.CollectAndCompare(svc.CallsTotal, strings.NewReader(expected)))

	// Verify no errors recorded
	errCount := testutil.ToFloat64(svc.ErrorsTotal.WithLabelValues("rpc", "GetHealth", "TESTNET", "other"))
	assert.Equal(t, float64(0), errCount)
}

func TestInstrumentedRPCService_GetHealth_RecordsErrorMetrics(t *testing.T) {
	svc, _ := newTestService(t)
	mock := &utils.MockRPCService{
		GetHealthFunc: func(network string) (types.GetHealthResponse, error) {
			return types.GetHealthResponse{}, fmt.Errorf("connection refused")
		},
	}
	instrumented := NewInstrumentedRPCService(mock, svc)

	_, err := instrumented.GetHealth(context.Background(), types.PUBLIC)
	require.Error(t, err)

	errCount := testutil.ToFloat64(svc.ErrorsTotal.WithLabelValues("rpc", "GetHealth", "PUBLIC", "other"))
	assert.Equal(t, float64(1), errCount)
}

func TestInstrumentedRPCService_GetHealth_TimeoutError(t *testing.T) {
	svc, _ := newTestService(t)
	mock := &utils.MockRPCService{
		GetHealthFunc: func(network string) (types.GetHealthResponse, error) {
			return types.GetHealthResponse{}, context.DeadlineExceeded
		},
	}
	instrumented := NewInstrumentedRPCService(mock, svc)

	_, err := instrumented.GetHealth(context.Background(), types.PUBLIC)
	require.Error(t, err)

	timeoutCount := testutil.ToFloat64(svc.ErrorsTotal.WithLabelValues("rpc", "GetHealth", "PUBLIC", "timeout"))
	assert.Equal(t, float64(1), timeoutCount)
}

func TestInstrumentedRPCService_GetLedgerEntries_PassesThrough(t *testing.T) {
	svc, _ := newTestService(t)
	expectedEntries := []types.LedgerEntryMap{{Account: types.AccountInfo{}}}
	mock := &utils.MockRPCService{
		GetLedgerEntryOverride: expectedEntries,
	}
	instrumented := NewInstrumentedRPCService(mock, svc)

	result, err := instrumented.GetLedgerEntries(context.Background(), []string{"key1"}, types.TESTNET)
	require.NoError(t, err)
	assert.Equal(t, expectedEntries, result)

	callCount := testutil.ToFloat64(svc.CallsTotal.WithLabelValues("rpc", "GetLedgerEntries", "TESTNET"))
	assert.Equal(t, float64(1), callCount)
}

func TestInstrumentedRPCService_GetLedgerEntries_ErrorPassesThrough(t *testing.T) {
	svc, _ := newTestService(t)
	expectedErr := fmt.Errorf("rpc error")
	mock := &utils.MockRPCService{
		GetLedgerEntryError: expectedErr,
	}
	instrumented := NewInstrumentedRPCService(mock, svc)

	result, err := instrumented.GetLedgerEntries(context.Background(), []string{"key1"}, types.FUTURENET)
	require.ErrorIs(t, err, expectedErr)
	assert.Nil(t, result)

	errCount := testutil.ToFloat64(svc.ErrorsTotal.WithLabelValues("rpc", "GetLedgerEntries", "FUTURENET", "other"))
	assert.Equal(t, float64(1), errCount)
}

func TestInstrumentedRPCService_Name_DelegatesToInner(t *testing.T) {
	svc, _ := newTestService(t)
	mock := &utils.MockRPCService{}
	instrumented := NewInstrumentedRPCService(mock, svc)

	assert.Equal(t, "mock-rpc", instrumented.Name())
}

func TestInstrumentedRPCService_SimulateTx_RecordsMetrics(t *testing.T) {
	svc, _ := newTestService(t)
	mock := &utils.MockRPCService{}
	instrumented := NewInstrumentedRPCService(mock, svc)

	_, err := instrumented.SimulateTx(context.Background(), nil, types.TESTNET)
	require.NoError(t, err)

	callCount := testutil.ToFloat64(svc.CallsTotal.WithLabelValues("rpc", "SimulateTx", "TESTNET"))
	assert.Equal(t, float64(1), callCount)
}

func TestInstrumentedWalletBackendService_GetBalances_RecordsMetrics(t *testing.T) {
	svc, _ := newTestService(t)
	expectedResult := map[string]string{"balance": "100"}
	mock := &utils.MockWalletBackendService{
		GetBalancesOverride: expectedResult,
	}
	instrumented := NewInstrumentedWalletBackendService(mock, svc)

	result, err := instrumented.GetBalancesByAccountAddresses(context.Background(), []string{"addr1"}, types.PUBLIC)
	require.NoError(t, err)
	assert.Equal(t, expectedResult, result)

	callCount := testutil.ToFloat64(svc.CallsTotal.WithLabelValues("wallet-backend", "GetBalancesByAccountAddresses", "PUBLIC"))
	assert.Equal(t, float64(1), callCount)
}

func TestInstrumentedWalletBackendService_GetBalances_ErrorRecordsMetrics(t *testing.T) {
	svc, _ := newTestService(t)
	mock := &utils.MockWalletBackendService{
		GetBalancesError: fmt.Errorf("backend error"),
	}
	instrumented := NewInstrumentedWalletBackendService(mock, svc)

	_, err := instrumented.GetBalancesByAccountAddresses(context.Background(), []string{"addr1"}, types.PUBLIC)
	require.Error(t, err)

	errCount := testutil.ToFloat64(svc.ErrorsTotal.WithLabelValues("wallet-backend", "GetBalancesByAccountAddresses", "PUBLIC", "other"))
	assert.Equal(t, float64(1), errCount)
}

func TestInstrumentedWalletBackendService_Name_DelegatesToInner(t *testing.T) {
	svc, _ := newTestService(t)
	mock := &utils.MockWalletBackendService{}
	instrumented := NewInstrumentedWalletBackendService(mock, svc)

	assert.Equal(t, "mock-wallet-backend", instrumented.Name())
}

func TestInstrumentedWalletBackendService_GetHealth_RecordsMetrics(t *testing.T) {
	svc, _ := newTestService(t)
	mock := &utils.MockWalletBackendService{}
	instrumented := NewInstrumentedWalletBackendService(mock, svc)

	result, err := instrumented.GetHealth(context.Background(), types.TESTNET)
	require.NoError(t, err)
	assert.Equal(t, types.StatusHealthy, result.Status)

	callCount := testutil.ToFloat64(svc.CallsTotal.WithLabelValues("wallet-backend", "GetHealth", "TESTNET"))
	assert.Equal(t, float64(1), callCount)
}

func TestInstrumentedRPCService_DurationRecorded(t *testing.T) {
	svc, reg := newTestService(t)
	mock := &utils.MockRPCService{}
	instrumented := NewInstrumentedRPCService(mock, svc)

	_, _ = instrumented.GetHealth(context.Background(), types.TESTNET)

	// Verify histogram has observations
	count := testutil.CollectAndCount(reg, "freighter_service_call_duration_seconds")
	assert.Equal(t, 1, count)
}
