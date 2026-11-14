// ABOUTME: Tests for the cross-chain deposit handlers, run against the real service with mocked Cross Mesh and RPC.
// ABOUTME: Test names carry the TR numbers they cover so `go test -run TR16` finds them.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/services"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

// Cross Mesh's published testnet vector: this account at index 0 derives
// ccDerived from the pinned testnet factory and implementation.
const (
	ccAccount       = "GBRAD6AQVETCVZDBT3TLU3KXGPQIXOIQX5DLJYKHNZ4ZF6AWYQXEZGQD"
	ccDerived       = "0x93845A25A3a5F9b15c25532797CE56fD4851Fcce"
	ccFastAddress   = "0x36b10Bac9dfb4C10947dCd49cB480594F0a1124B"
	ccTestnetIssuer = "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5"
	ccCreatedAt     = "2026-11-01T00:00:00Z"
)

var ccConfig = config.CrossMeshConfig{TestnetURL: "http://crossmesh.test", TestnetAPIKey: "test-key"}

func int64Ptr(n int64) *int64 { return &n }

func ccChains() *types.CrossMeshChains {
	return &types.CrossMeshChains{Chains: []types.CrossMeshChain{
		{
			Chain: "polygon-amoy", ChainID: 80002, Status: "active", USDCAddress: "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582",
			Confirmations: 3, MinSettlementAmount: "1000000", Decimals: 6,
			Speeds: types.CrossMeshSpeeds{Standard: types.CrossMeshSpeed{EstimatedDeliverySeconds: int64Ptr(1200)}},
			Fees:   &types.CrossMeshChainFees{SetupFee: "100000", BaseFee: "50000", FeeRatePPM: "0", Decimals: 6},
		},
		{
			Chain: "ethereum-sepolia", ChainID: 11155111, Status: "suspended", USDCAddress: "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
			Confirmations: 12, MinSettlementAmount: "5000000", Decimals: 6,
		},
	}}
}

func ccStandardRow(address string) *types.CrossMeshDepositAddress {
	return &types.CrossMeshDepositAddress{
		DepositAddress: address, Recipient: ccAccount, DeliveryAsset: "USDC", Speed: "standard", Index: 0, CreatedAt: ccCreatedAt,
	}
}

func ccLedgerEntry(t *testing.T, data xdr.LedgerEntryData) string {
	t.Helper()
	b64, err := xdr.MarshalBase64(data)
	require.NoError(t, err)
	return b64
}

// ccLedger answers the trustline check's getLedgerEntries call (keys[0] is
// the account, keys[1] the USDC trustline). A nil limit leaves the trustline
// out; account=false leaves both out.
func ccLedger(t *testing.T, account bool, limit *xdr.Int64) *utils.MockRPCService {
	t.Helper()
	entries := func(keys []string) map[string]string {
		out := map[string]string{}
		if !account {
			return out
		}
		out[keys[0]] = ccLedgerEntry(t, xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeAccount, Account: &xdr.AccountEntry{AccountId: xdr.MustAddress(ccAccount)}})
		if limit != nil {
			out[keys[1]] = ccLedgerEntry(t, xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeTrustline, TrustLine: &xdr.TrustLineEntry{
				AccountId: xdr.MustAddress(ccAccount),
				Asset:     xdr.MustNewCreditAsset("USDC", ccTestnetIssuer).ToTrustLineAsset(),
				Limit:     *limit,
			}})
		}
		return out
	}
	return &utils.MockRPCService{GetLedgerEntriesXDRFunc: func(keys []string, _ string) (map[string]string, error) {
		return entries(keys), nil
	}}
}

func limit(n xdr.Int64) *xdr.Int64 { return &n }

// ccFixture wires the handler to the real service. Tests change the mocks'
// answers before calling serve.
type ccFixture struct {
	chains        *types.CrossMeshChains
	chainsErr     error
	chainsCalls   int
	row           *types.CrossMeshDepositAddress
	rowErr        error
	upstreamCalls int
	rpc           *utils.MockRPCService
	cache         services.JSONCache
	metrics       *metrics.CrossChain

	// Deposits route: pages by cursor ("" is the first page) and settlements
	// by id. settlementErr, when set, is consulted first with the 1-based call
	// count for that id. A settlement missing from the map is a 404
	// settlement_not_found. Settlement calls run concurrently, so mu guards
	// settlementCalls.
	pages           map[string]*types.CrossMeshDepositPage
	listCursors     []string
	listLimits      []int
	settlements     map[string]*types.CrossMeshSettlement
	settlementErr   func(id string, call int) error
	mu              sync.Mutex
	settlementCalls map[string]int
}

func newCCFixture(t *testing.T) *ccFixture {
	t.Helper()
	return &ccFixture{
		chains:  ccChains(),
		row:     ccStandardRow(ccDerived),
		rpc:     ccLedger(t, true, limit(1)),
		metrics: metrics.NewCrossChain(prometheus.NewRegistry()),

		settlementCalls: map[string]int{},
	}
}

func (f *ccFixture) serve(method, target, body string) *httptest.ResponseRecorder {
	fetch := func(context.Context, string, string) (*types.CrossMeshDepositAddress, error) {
		f.upstreamCalls++
		return f.row, f.rowErr
	}
	cm := &utils.MockCrossMeshService{
		GetChainsFunc: func(context.Context, string) (*types.CrossMeshChains, error) {
			f.chainsCalls++
			return f.chains, f.chainsErr
		},
		CreateDepositAddressFunc: fetch,
		LookupDepositAddressFunc: fetch,
		ListDepositsFunc: func(_ context.Context, _, _, cursor string, limit int) (*types.CrossMeshDepositPage, error) {
			f.listCursors = append(f.listCursors, cursor)
			f.listLimits = append(f.listLimits, limit)
			return f.pages[cursor], nil
		},
		GetSettlementFunc: func(_ context.Context, _, id string) (*types.CrossMeshSettlement, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.settlementCalls[id]++
			if f.settlementErr != nil {
				if err := f.settlementErr(id, f.settlementCalls[id]); err != nil {
					return nil, err
				}
			}
			st, ok := f.settlements[id]
			if !ok {
				return nil, ccUpstreamError(http.StatusNotFound, "settlement_not_found")
			}
			return st, nil
		},
	}
	h := NewCrossChainDepositHandler(services.NewCrossChainDepositService(cm, f.rpc, f.cache, ccConfig, f.metrics))
	mux := http.NewServeMux()
	mux.Handle("GET /chains", CustomHandler(h.GetChains))
	mux.Handle("POST /addresses", CustomHandler(h.CreateAddress))
	mux.Handle("GET /accounts/{account}/address", CustomHandler(h.LookupAddress))
	mux.Handle("GET /accounts/{account}/deposits", CustomHandler(h.ListDeposits))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

// ccAddressRoute is one of the two address routes, called for account.
type ccAddressRoute struct {
	name string
	call func(f *ccFixture, account, network string) *httptest.ResponseRecorder
}

var ccAddressRoutes = []ccAddressRoute{
	{"POST addresses", func(f *ccFixture, account, network string) *httptest.ResponseRecorder {
		return f.serve(http.MethodPost, "/addresses?network="+network, fmt.Sprintf(`{"account":%q}`, account))
	}},
	{"GET address", func(f *ccFixture, account, network string) *httptest.ResponseRecorder {
		return f.serve(http.MethodGet, "/accounts/"+account+"/address?network="+network, "")
	}},
}

type ccErrorBody struct {
	StatusCode int            `json:"statusCode"`
	Extras     map[string]any `json:"extras"`
}

// requireCCError asserts the response status and machine-readable code and
// returns the error extras.
func requireCCError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) map[string]any {
	t.Helper()
	require.Equal(t, status, rec.Code, rec.Body.String())
	var body ccErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, code, body.Extras["code"], rec.Body.String())
	return body.Extras
}

// captureLogs sends the global logger to a buffer for the rest of the test.
// Tests that use it must not run in parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	logger.SetOutput(&logs)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })
	return &logs
}

// ccUpstreamError is a Cross Mesh error answer as the client returns it.
func ccUpstreamError(status int, code string) error {
	return &metrics.UpstreamError{Kind: "http_error", Code: status, Err: &services.CrossMeshError{Status: status, Code: code}}
}

// ccMapCache is an in-memory JSON cache. ttls records each key's TTL.
type ccMapCache struct {
	mu      sync.Mutex
	entries map[string][]byte
	ttls    map[string]time.Duration
}

func (c *ccMapCache) MGetJSON(_ context.Context, keys []string, makeDest func() any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]any{}
	for _, k := range keys {
		if raw, ok := c.entries[k]; ok {
			dest := makeDest()
			if err := json.Unmarshal(raw, dest); err != nil {
				return nil, err
			}
			out[k] = dest
		}
	}
	return out, nil
}

func (c *ccMapCache) SetJSON(_ context.Context, key string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = raw
	if c.ttls == nil {
		c.ttls = map[string]time.Duration{}
	}
	c.ttls[key] = ttl
	return nil
}

func TestChains_TR9_11_12_PassThroughAndCache(t *testing.T) {
	t.Run("passes chain fields through with nullable estimate and fees", func(t *testing.T) {
		f := newCCFixture(t)
		rec := f.serve(http.MethodGet, "/chains?network=TESTNET", "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"data":{"chains":[
			{"chain":"polygon-amoy","chain_id":80002,"status":"active","usdc_address":"0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582",
			 "confirmations":3,"min_settlement_amount":"1000000","decimals":6,"estimated_delivery_seconds":1200,
			 "fees":{"setup_fee":"100000","base_fee":"50000","fee_rate_ppm":"0","decimals":6}},
			{"chain":"ethereum-sepolia","chain_id":11155111,"status":"suspended","usdc_address":"0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
			 "confirmations":12,"min_settlement_amount":"5000000","decimals":6,"estimated_delivery_seconds":null,"fees":null}
		]}}`, rec.Body.String())
	})

	t.Run("cache hit avoids upstream", func(t *testing.T) {
		f := newCCFixture(t)
		f.cache = &ccMapCache{entries: map[string][]byte{}}
		for range 2 {
			require.Equal(t, http.StatusOK, f.serve(http.MethodGet, "/chains?network=TESTNET", "").Code)
		}
		assert.Equal(t, 1, f.chainsCalls)
	})

	t.Run("works without redis", func(t *testing.T) {
		f := newCCFixture(t)
		for range 2 {
			require.Equal(t, http.StatusOK, f.serve(http.MethodGet, "/chains?network=TESTNET", "").Code)
		}
		assert.Equal(t, 2, f.chainsCalls)
	})

	t.Run("upstream error returns 502 with upstream extras", func(t *testing.T) {
		logs := captureLogs(t)
		f := newCCFixture(t)
		f.chainsErr = &metrics.UpstreamError{Kind: "http_error", Code: 500, Err: &services.CrossMeshError{Status: 500, Code: "internal_error", RequestID: "req_1"}}
		extras := requireCCError(t, f.serve(http.MethodGet, "/chains?network=TESTNET", ""), http.StatusBadGateway, "upstream_error")
		assert.Equal(t, "internal_error", extras["upstream_code"])
		assert.Equal(t, "req_1", extras["request_id"])
		assert.Contains(t, logs.String(), "cross-chain deposit request failed")
		assert.NotContains(t, logs.String(), ccConfig.TestnetAPIKey)
	})
}

func TestCrossChain_UnconfiguredNetworkReturns404(t *testing.T) {
	for _, network := range []string{types.PUBLIC, types.FUTURENET, "", "MOON"} {
		f := newCCFixture(t)
		requireCCError(t, f.serve(http.MethodGet, "/chains?network="+network, ""), http.StatusNotFound, "network_not_supported")
		for _, rt := range ccAddressRoutes {
			requireCCError(t, rt.call(f, ccAccount, network), http.StatusNotFound, "network_not_supported")
		}
		requireCCError(t, f.serve(http.MethodGet, "/accounts/"+ccAccount+"/deposits?network="+network, ""), http.StatusNotFound, "network_not_supported")
		assert.Zero(t, f.chainsCalls+f.upstreamCalls+len(f.listCursors), network)
	}
}

func TestAddress_TR14_AllSuspendedReturns409(t *testing.T) {
	for _, rt := range ccAddressRoutes {
		t.Run(rt.name, func(t *testing.T) {
			f := newCCFixture(t)
			f.chains = &types.CrossMeshChains{Chains: []types.CrossMeshChain{{Status: "suspended"}, {Status: "suspended"}}}
			requireCCError(t, rt.call(f, ccAccount, types.TESTNET), http.StatusConflict, "no_active_chain")
			assert.Zero(t, f.upstreamCalls)
		})
	}
}

func TestAddress_TR15_RejectsNonGAccounts(t *testing.T) {
	muxed, err := strkey.Encode(strkey.VersionByteMuxedAccount, make([]byte, 40))
	require.NoError(t, err)
	contract, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
	require.NoError(t, err)

	for _, rt := range ccAddressRoutes {
		for name, account := range map[string]string{"muxed": muxed, "contract": contract, "garbage": "not-an-account"} {
			t.Run(rt.name+"/"+name, func(t *testing.T) {
				f := newCCFixture(t)
				requireCCError(t, rt.call(f, account, types.TESTNET), http.StatusBadRequest, "invalid_account")
				assert.Zero(t, f.chainsCalls+f.upstreamCalls)
			})
		}
	}

	t.Run("POST with a malformed body", func(t *testing.T) {
		f := newCCFixture(t)
		requireCCError(t, f.serve(http.MethodPost, "/addresses?network=TESTNET", "{"), http.StatusBadRequest, "invalid_account")
	})

	t.Run("upstream invalid_stellar_address", func(t *testing.T) {
		f := newCCFixture(t)
		f.rowErr = &metrics.UpstreamError{Kind: "http_error", Code: 400, Err: &services.CrossMeshError{Status: 400, Code: "invalid_stellar_address"}}
		requireCCError(t, ccAddressRoutes[0].call(f, ccAccount, types.TESTNET), http.StatusBadRequest, "invalid_account")
	})
}

func TestAddress_TR16_MissingTrustlineReturns422(t *testing.T) {
	for _, rt := range ccAddressRoutes {
		t.Run(rt.name, func(t *testing.T) {
			f := newCCFixture(t)
			f.rpc = ccLedger(t, true, nil)
			extras := requireCCError(t, rt.call(f, ccAccount, types.TESTNET), http.StatusUnprocessableEntity, "usdc_trustline_missing")
			assert.Equal(t, "USDC", extras["asset_code"])
			assert.Equal(t, ccTestnetIssuer, extras["asset_issuer"])
			assert.Zero(t, f.upstreamCalls)
		})
	}
}

func TestAddress_TR16_LimitZero(t *testing.T) {
	for _, rt := range ccAddressRoutes {
		t.Run(rt.name, func(t *testing.T) {
			f := newCCFixture(t)
			f.rpc = ccLedger(t, true, limit(0))
			requireCCError(t, rt.call(f, ccAccount, types.TESTNET), http.StatusUnprocessableEntity, "usdc_trustline_missing")
		})
	}
}

func TestAddress_TR8_LookupAlsoChecksTrustline(t *testing.T) {
	f := newCCFixture(t)
	f.rpc = ccLedger(t, true, nil)
	requireCCError(t, ccAddressRoutes[1].call(f, ccAccount, types.TESTNET), http.StatusUnprocessableEntity, "usdc_trustline_missing")
	assert.Zero(t, f.upstreamCalls, "the lookup must not run when the trustline is missing")

	f.rpc = ccLedger(t, true, limit(1))
	assert.Equal(t, http.StatusOK, ccAddressRoutes[1].call(f, ccAccount, types.TESTNET).Code)
}

func TestAddress_TR18_26_GetOrCreateReturnsDerivedAddress(t *testing.T) {
	f := newCCFixture(t)
	f.row = ccStandardRow(strings.ToLower(ccDerived))
	want := fmt.Sprintf(`{"data":{"deposit_address":%q,"account":%q,"index":0,"created_at":%q}}`, ccDerived, ccAccount, ccCreatedAt)
	for range 2 {
		rec := ccAddressRoutes[0].call(f, ccAccount, types.TESTNET)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.JSONEq(t, want, rec.Body.String())
	}
	rec := ccAddressRoutes[1].call(f, ccAccount, types.TESTNET)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, want, rec.Body.String())
}

func TestLookup_TR10_NoAddressReturns404(t *testing.T) {
	f := newCCFixture(t)
	f.row = nil
	requireCCError(t, ccAddressRoutes[1].call(f, ccAccount, types.TESTNET), http.StatusNotFound, "no_deposit_address")
}

func TestAddress_TR24_MismatchReturns502AndCounts(t *testing.T) {
	logs := captureLogs(t)
	f := newCCFixture(t)
	f.row = ccStandardRow(ccFastAddress)
	for _, rt := range ccAddressRoutes {
		requireCCError(t, rt.call(f, ccAccount, types.TESTNET), http.StatusBadGateway, "verification_failed")
	}
	assert.Equal(t, float64(2), testutil.ToFloat64(f.metrics.VerificationFailures.WithLabelValues(types.TESTNET)))
	assert.Equal(t, 2, strings.Count(logs.String(), "deposit address failed verification"))
	assert.NotContains(t, logs.String(), ccConfig.TestnetAPIKey)
}

func TestAddress_TR25_VerifiesOnEveryResponse(t *testing.T) {
	captureLogs(t)
	for _, rt := range ccAddressRoutes {
		t.Run(rt.name, func(t *testing.T) {
			f := newCCFixture(t)
			require.Equal(t, http.StatusOK, rt.call(f, ccAccount, types.TESTNET).Code)
			f.row = ccStandardRow(ccFastAddress)
			requireCCError(t, rt.call(f, ccAccount, types.TESTNET), http.StatusBadGateway, "verification_failed")
			assert.Equal(t, 2, f.upstreamCalls, "each response is fetched and verified, never cached")
		})
	}
}

func TestAddress_TR5_ErrorClasses(t *testing.T) {
	captureLogs(t)
	upstream := func(status int, code string) error {
		return &metrics.UpstreamError{Kind: "http_error", Code: status, Err: &services.CrossMeshError{Status: status, Code: code, RequestID: "req_9"}}
	}
	cases := map[string]struct {
		setup  func(t *testing.T, f *ccFixture)
		status int
		code   string
	}{
		"account not found": {func(t *testing.T, f *ccFixture) { f.rpc = ccLedger(t, false, nil) }, http.StatusUnprocessableEntity, "account_not_found"},
		"rpc error": {func(_ *testing.T, f *ccFixture) {
			f.rpc = &utils.MockRPCService{GetLedgerEntriesXDRFunc: func([]string, string) (map[string]string, error) {
				return nil, fmt.Errorf("rpc down")
			}}
		}, http.StatusServiceUnavailable, "upstream_unavailable"},
		"upstream 5xx":            {func(_ *testing.T, f *ccFixture) { f.rowErr = upstream(500, "internal_error") }, http.StatusServiceUnavailable, "upstream_unavailable"},
		"upstream 4xx":            {func(_ *testing.T, f *ccFixture) { f.rowErr = upstream(422, "address_limit_reached") }, http.StatusBadGateway, "upstream_error"},
		"upstream decode failure": {func(_ *testing.T, f *ccFixture) { f.rowErr = fmt.Errorf("decoding crossmesh response: unexpected EOF") }, http.StatusBadGateway, "upstream_error"},
		"wrong speed row": {func(_ *testing.T, f *ccFixture) {
			f.row = ccStandardRow(ccDerived)
			f.row.Speed = "fast"
		}, http.StatusBadGateway, "upstream_error"},
		"chains 5xx": {func(_ *testing.T, f *ccFixture) { f.chainsErr = upstream(503, "unavailable") }, http.StatusBadGateway, "upstream_error"},
		"timeout": {func(_ *testing.T, f *ccFixture) {
			f.rowErr = fmt.Errorf("crossmesh CreateDepositAddress: %w", context.DeadlineExceeded)
		}, http.StatusGatewayTimeout, "upstream_timeout"},
	}
	for name, tc := range cases {
		for _, rt := range ccAddressRoutes {
			t.Run(name+"/"+rt.name, func(t *testing.T) {
				f := newCCFixture(t)
				tc.setup(t, f)
				extras := requireCCError(t, rt.call(f, ccAccount, types.TESTNET), tc.status, tc.code)
				if name == "upstream 4xx" {
					assert.Equal(t, "address_limit_reached", extras["upstream_code"])
					assert.Equal(t, "req_9", extras["request_id"])
				}
			})
		}
	}
}
