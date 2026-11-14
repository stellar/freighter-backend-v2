// ABOUTME: Integration tests for the cross-chain deposit routes on TESTNET, against the fake Cross Mesh and the standalone network.
// ABOUTME: Test names carry the requirement (TR) numbers they cover; recipients pick the fake's canned scenarios.

package integrationtests

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stellar/go-stellar-sdk/clients/rpcclient"
	"github.com/stellar/go-stellar-sdk/keypair"
	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/suite"

	"github.com/stellar/freighter-backend-v2/internal/integrationtests/infrastructure"
	"github.com/stellar/freighter-backend-v2/internal/services"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

// Recipients with canned deposit histories in the fake Cross Mesh
// (infrastructure/fakecrossmesh/data.go). Any other recipient has none.
const (
	happyPathRecipient             = "GDOAELTTQ52OGFCEGWYE6UZOYWK3PCBKDTJ4WM3ZIOVJUQ7BIBJW5WK2"
	twoRoundRecipient              = "GC3G7ZKLTOIDT6MILJGH7FFBAPY4GG4WPSXTDCXU4AWNFQSYTGCJRH7M"
	settlementUnavailableRecipient = "GBVOVVM5PN7FCLLF3ZQIVSIINWF62GHCHM7R5FDZLKSKGQZS4MGKZWLC"
)

type CrossChainDepositTestSuite struct {
	suite.Suite
	freighterContainer          *infrastructure.FreighterBackendContainer
	suspendedFreighterContainer *infrastructure.FreighterBackendContainer
	rpcContainer                *infrastructure.TestContainer

	baseURL      string
	suspendedURL string
	rpc          *rpcclient.Client
	testnetEnv   services.CrossMeshEnv

	// Funded accounts on the standalone network.
	noTrustline string
	trustlined  string
	noAddress   string // trustlined, never given a deposit address
}

func (s *CrossChainDepositTestSuite) SetupSuite() {
	ctx := context.Background()
	var err error
	s.baseURL, err = s.freighterContainer.GetConnectionString(ctx)
	s.Require().NoError(err)
	s.suspendedURL, err = s.suspendedFreighterContainer.GetConnectionString(ctx)
	s.Require().NoError(err)
	rpcURL, err := s.rpcContainer.GetConnectionString(ctx)
	s.Require().NoError(err)
	s.rpc = rpcclient.NewClient(rpcURL, nil)

	var ok bool
	s.testnetEnv, ok = services.CrossMeshEnvFor(types.TESTNET)
	s.Require().True(ok)

	// The backend's TESTNET RPC is the standalone network, so the USDC
	// trustline check reads accounts created here. changeTrust needs the
	// issuer to exist; its key is public, so root can create it.
	root, ok := keypair.Master(standaloneNetworkPassphrase).(*keypair.Full)
	s.Require().True(ok)
	noTrustline, trustlined, noAddress := keypair.MustRandom(), keypair.MustRandom(), keypair.MustRandom()
	s.noTrustline, s.trustlined, s.noAddress = noTrustline.Address(), trustlined.Address(), noAddress.Address()

	s.submit(root, []txnbuild.Operation{
		&txnbuild.CreateAccount{Destination: s.testnetEnv.USDCIssuer, Amount: "100"},
		&txnbuild.CreateAccount{Destination: s.noTrustline, Amount: "100"},
		&txnbuild.CreateAccount{Destination: s.trustlined, Amount: "100"},
		&txnbuild.CreateAccount{Destination: s.noAddress, Amount: "100"},
	})
	usdc := txnbuild.CreditAsset{Code: "USDC", Issuer: s.testnetEnv.USDCIssuer}.MustToChangeTrustAsset()
	s.submit(trustlined, []txnbuild.Operation{
		&txnbuild.ChangeTrust{Line: usdc},
		&txnbuild.ChangeTrust{Line: usdc, SourceAccount: s.noAddress},
	}, noAddress)
}

// submit signs ops from source (plus any extra signers) and waits for the
// transaction to succeed on the standalone network.
func (s *CrossChainDepositTestSuite) submit(source *keypair.Full, ops []txnbuild.Operation, signers ...*keypair.Full) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	account, err := s.rpc.LoadAccount(ctx, source.Address())
	s.Require().NoError(err)
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        account,
		IncrementSequenceNum: true,
		Operations:           ops,
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
	})
	s.Require().NoError(err)
	tx, err = tx.Sign(standaloneNetworkPassphrase, append([]*keypair.Full{source}, signers...)...)
	s.Require().NoError(err)
	envelope, err := tx.Base64()
	s.Require().NoError(err)

	sent, err := s.rpc.SendTransaction(ctx, protocol.SendTransactionRequest{Transaction: envelope})
	s.Require().NoError(err)
	s.Require().Equal("PENDING", sent.Status, "sendTransaction: %+v", sent)
	result, err := s.rpc.PollTransaction(ctx, sent.Hash)
	s.Require().NoError(err)
	s.Require().Equal(protocol.TransactionStatusSuccess, result.Status, "result: %s", result.ResultXDR)
}

// call sends one request to a backend and returns the status and body.
func (s *CrossChainDepositTestSuite) call(method, url, body string) (int, string) {
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	s.Require().NoError(err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	s.Require().NoError(err)
	return resp.StatusCode, string(raw)
}

func (s *CrossChainDepositTestSuite) postAddress(base, account, network string) (int, string) {
	return s.call(http.MethodPost, fmt.Sprintf("%s/api/v1/cross-chain-deposit/addresses?network=%s", base, network), fmt.Sprintf(`{"account":%q}`, account))
}

func (s *CrossChainDepositTestSuite) getAddress(base, account string) (int, string) {
	return s.call(http.MethodGet, fmt.Sprintf("%s/api/v1/cross-chain-deposit/accounts/%s/address?network=TESTNET", base, account), "")
}

func (s *CrossChainDepositTestSuite) getDeposits(account string) (int, string) {
	return s.call(http.MethodGet, fmt.Sprintf("%s/api/v1/cross-chain-deposit/accounts/%s/deposits?network=TESTNET", s.baseURL, account), "")
}

// requireErrorCode asserts an error response's status and extras.code.
func (s *CrossChainDepositTestSuite) requireErrorCode(status int, body string, wantStatus int, wantCode string) {
	s.Require().Equal(wantStatus, status, "body: %s", body)
	var envelope struct {
		Extras struct {
			Code string `json:"code"`
		} `json:"extras"`
	}
	s.Require().NoError(json.Unmarshal([]byte(body), &envelope), "body: %s", body)
	s.Require().Equal(wantCode, envelope.Extras.Code, "body: %s", body)
}

// requireDerivedAddress asserts a 200 address response carries the address
// derived locally for account at the response's index, in EIP-55 form.
func (s *CrossChainDepositTestSuite) requireDerivedAddress(status int, body, account string) string {
	s.Require().Equal(http.StatusOK, status, "body: %s", body)
	var envelope struct {
		Data struct {
			DepositAddress string `json:"deposit_address"`
			Account        string `json:"account"`
			Index          uint64 `json:"index"`
			CreatedAt      string `json:"created_at"`
		} `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(body), &envelope), "body: %s", body)
	want, err := services.ComputeDepositAddress(account, envelope.Data.Index, false, s.testnetEnv.Factory, s.testnetEnv.Implementation)
	s.Require().NoError(err)
	s.Require().Equal(want, envelope.Data.DepositAddress)
	s.Require().Equal(account, envelope.Data.Account)
	s.Require().NotEmpty(envelope.Data.CreatedAt)
	return envelope.Data.DepositAddress
}

func (s *CrossChainDepositTestSuite) TestFeatureFlags_TR2_ListsConfiguredNetworksOnly() {
	status, body := s.call(http.MethodGet, s.baseURL+"/api/v1/feature-flags", "")
	s.Require().Equal(http.StatusOK, status, "body: %s", body)
	var flags struct {
		Networks []string `json:"cross_chain_deposit_networks"`
	}
	s.Require().NoError(json.Unmarshal([]byte(body), &flags))
	s.Require().Equal([]string{"TESTNET"}, flags.Networks)
}

func (s *CrossChainDepositTestSuite) TestChains_TR9_TR11_TR12_TR34_TR43_TR46_TR47_PassesThroughChainFields() {
	status, body := s.call(http.MethodGet, s.baseURL+"/api/v1/cross-chain-deposit/chains?network=TESTNET", "")
	s.Require().Equal(http.StatusOK, status, "body: %s", body)
	s.Require().JSONEq(`{"data":{"chains":[
		{"chain":"ethereum","chain_id":11155111,"status":"active","usdc_address":"0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
		 "confirmations":3,"min_settlement_amount":"1000000","decimals":6,"estimated_delivery_seconds":1080,
		 "fees":{"setup_fee":"100000","base_fee":"50000","fee_rate_ppm":"0","decimals":6}},
		{"chain":"polygon","chain_id":80002,"status":"active","usdc_address":"0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582",
		 "confirmations":3,"min_settlement_amount":"1000000","decimals":6,"estimated_delivery_seconds":null,"fees":null}
	]}}`, body)
}

func (s *CrossChainDepositTestSuite) TestChains_UnconfiguredNetworksReturn404() {
	for _, network := range []string{"PUBLIC", "FUTURENET"} {
		status, body := s.call(http.MethodGet, s.baseURL+"/api/v1/cross-chain-deposit/chains?network="+network, "")
		s.requireErrorCode(status, body, http.StatusNotFound, "network_not_supported")
	}
}

func (s *CrossChainDepositTestSuite) TestAddress_TR8_TR16_MissingTrustlineReturns422OnBothRoutes() {
	status, body := s.postAddress(s.baseURL, s.noTrustline, "TESTNET")
	s.requireErrorCode(status, body, http.StatusUnprocessableEntity, "usdc_trustline_missing")
	s.Require().Contains(body, s.testnetEnv.USDCIssuer)

	status, body = s.getAddress(s.baseURL, s.noTrustline)
	s.requireErrorCode(status, body, http.StatusUnprocessableEntity, "usdc_trustline_missing")
}

func (s *CrossChainDepositTestSuite) TestAddress_TR8_TR18_TR19_TR20to26_TR28_CreateThenLookupReturnDerivedAddress() {
	status, body := s.postAddress(s.baseURL, s.trustlined, "TESTNET")
	created := s.requireDerivedAddress(status, body, s.trustlined)

	status, body = s.postAddress(s.baseURL, s.trustlined, "TESTNET")
	s.Require().Equal(created, s.requireDerivedAddress(status, body, s.trustlined), "POST is get-or-create")

	status, body = s.getAddress(s.baseURL, s.trustlined)
	s.Require().Equal(created, s.requireDerivedAddress(status, body, s.trustlined))
}

func (s *CrossChainDepositTestSuite) TestAddress_TR10_LookupWithoutAddressReturns404() {
	status, body := s.getAddress(s.baseURL, s.noAddress)
	s.requireErrorCode(status, body, http.StatusNotFound, "no_deposit_address")
}

func (s *CrossChainDepositTestSuite) TestAddress_TR14_AllChainsSuspendedReturns409OnBothRoutes() {
	status, body := s.postAddress(s.suspendedURL, s.trustlined, "TESTNET")
	s.requireErrorCode(status, body, http.StatusConflict, "no_active_chain")

	status, body = s.getAddress(s.suspendedURL, s.trustlined)
	s.requireErrorCode(status, body, http.StatusConflict, "no_active_chain")
}

func (s *CrossChainDepositTestSuite) TestAccountRoutes_TR15_MuxedAccountReturns400() {
	muxed, err := xdr.MuxedAccountFromAccountId(s.trustlined, 7)
	s.Require().NoError(err)
	m := muxed.Address()

	status, body := s.postAddress(s.baseURL, m, "TESTNET")
	s.requireErrorCode(status, body, http.StatusBadRequest, "invalid_account")
	status, body = s.getAddress(s.baseURL, m)
	s.requireErrorCode(status, body, http.StatusBadRequest, "invalid_account")
	status, body = s.getDeposits(m)
	s.requireErrorCode(status, body, http.StatusBadRequest, "invalid_account")
}

func (s *CrossChainDepositTestSuite) TestDeposits_TR35toTR41_TR51_HappyPathNewestFirstWithStatesAndFees() {
	status, body := s.getDeposits(happyPathRecipient)
	s.Require().Equal(http.StatusOK, status, "body: %s", body)
	// The fake serves these rows oldest-ish first; the backend orders them.
	// Fees land only on dep_happy_2, the newest deposit of stl_happy.
	s.Require().JSONEq(`{"data":{"deposits":[
		{"deposit_id":"dep_happy_5","status":"held","hold_reason":"external_settlement_unverified","hold_detail":"under review",
		 "user_state":"under_review","amount":"4000000","decimals":6,"source_chain":"ethereum","source_chain_id":11155111,
		 "source_tx":"0xe5","destination_tx":null,"settlement_id":null,
		 "created_at":"2026-11-05T10:00:00Z","updated_at":"2026-11-05T10:05:00Z","remaining_to_minimum":null,"fees":null},
		{"deposit_id":"dep_happy_4","status":"confirmed","hold_reason":null,"hold_detail":null,
		 "user_state":"pending","amount":"1000000","decimals":6,"source_chain":"ethereum","source_chain_id":11155111,
		 "source_tx":"0xe4","destination_tx":null,"settlement_id":null,
		 "created_at":"2026-11-04T10:00:00Z","updated_at":"2026-11-04T10:01:00Z","remaining_to_minimum":null,"fees":null},
		{"deposit_id":"dep_happy_3","status":"held","hold_reason":"recipient_not_ready","hold_detail":"no USDC trustline",
		 "user_state":"action_needed","amount":"3000000","decimals":6,"source_chain":"polygon","source_chain_id":80002,
		 "source_tx":"0xe3","destination_tx":null,"settlement_id":null,
		 "created_at":"2026-11-03T10:00:00Z","updated_at":"2026-11-03T10:05:00Z","remaining_to_minimum":null,"fees":null},
		{"deposit_id":"dep_happy_2","status":"completed","hold_reason":null,"hold_detail":null,
		 "user_state":"completed","amount":"2000000","decimals":6,"source_chain":"ethereum","source_chain_id":11155111,
		 "source_tx":"0xe2","destination_tx":"5a1e000000000000000000000000000000000000000000000000000000000001","settlement_id":"stl_happy",
		 "created_at":"2026-11-02T10:00:00Z","updated_at":"2026-11-02T10:30:00Z","remaining_to_minimum":null,
		 "fees":{"setup_fee":"100000","settlement_fee":"50000","cctp_fee":"700","total_fee":"150700","delivered_amount":"6849300","decimals":6}},
		{"deposit_id":"dep_happy_1","status":"completed","hold_reason":null,"hold_detail":null,
		 "user_state":"completed","amount":"5000000","decimals":6,"source_chain":"ethereum","source_chain_id":11155111,
		 "source_tx":"0xe1","destination_tx":"5a1e000000000000000000000000000000000000000000000000000000000001","settlement_id":"stl_happy",
		 "created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-02T10:30:00Z","remaining_to_minimum":null,"fees":null}
	]}}`, body)
}

func (s *CrossChainDepositTestSuite) TestDeposits_TR39_TwoRoundSettlementFeesSummedOnNewestRow() {
	status, body := s.getDeposits(twoRoundRecipient)
	s.Require().Equal(http.StatusOK, status, "body: %s", body)
	var envelope struct {
		Data struct {
			Deposits []struct {
				DepositID string          `json:"deposit_id"`
				Fees      json.RawMessage `json:"fees"`
			} `json:"deposits"`
		} `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(body), &envelope), "body: %s", body)
	deposits := envelope.Data.Deposits
	s.Require().Len(deposits, 2, "body: %s", body)
	// stl_round_1 is referenced by no deposit; stl_round_2 continues it.
	s.Require().Equal("dep_two_2", deposits[0].DepositID)
	s.Require().JSONEq(`{"setup_fee":"100000","settlement_fee":"100000","cctp_fee":"500","total_fee":"200500","delivered_amount":"4799500","decimals":6}`, string(deposits[0].Fees))
	s.Require().Equal("dep_two_1", deposits[1].DepositID)
	s.Require().JSONEq(`null`, string(deposits[1].Fees))
}

func (s *CrossChainDepositTestSuite) TestDeposits_EmptyHistoryReturnsEmptyList() {
	status, body := s.getDeposits(s.noAddress)
	s.Require().Equal(http.StatusOK, status, "body: %s", body)
	s.Require().JSONEq(`{"data":{"deposits":[]}}`, body)
}

func (s *CrossChainDepositTestSuite) TestDeposits_TR52_SettlementUpstream5xxReturns503() {
	status, body := s.getDeposits(settlementUnavailableRecipient)
	s.requireErrorCode(status, body, http.StatusServiceUnavailable, "upstream_unavailable")
}
