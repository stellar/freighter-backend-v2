// ABOUTME: Integration test for POST /api/v1/ledger-key/accounts against the
// ABOUTME: Protocol 28 stellar-rpc and stellar-core containers.

package integrationtests

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/stellar/freighter-backend-v2/internal/api/handlers"
	"github.com/stellar/freighter-backend-v2/internal/integrationtests/infrastructure"
)

// standaloneNetworkPassphrase mirrors NETWORK_PASSPHRASE in
// infrastructure/config/standalone-core.cfg. Stellar Core derives the genesis
// root account from the network ID, so deriving it here keeps the test correct
// if the passphrase ever changes — no address is hardcoded.
const standaloneNetworkPassphrase = "Standalone Network ; February 2017"

type LedgerKeyAccountsTestSuite struct {
	suite.Suite
	freighterContainer *infrastructure.FreighterBackendContainer
	connectionString   string
	rootAddress        string
}

func (s *LedgerKeyAccountsTestSuite) SetupSuite() {
	ctx := context.Background()
	var err error
	s.connectionString, err = s.freighterContainer.GetConnectionString(ctx)
	s.Require().NoError(err)
	s.Require().NotEmpty(s.connectionString)

	s.rootAddress = keypair.Master(standaloneNetworkPassphrase).Address()
	s.Require().NotEmpty(s.rootAddress)
}

// TestGetLedgerKeyAccountsDecodesGenesisRootAccount exercises the full
// getLedgerEntries path — ledger-key construction, the RPC round trip against
// Protocol 28, and the dataJson to types.AccountInfo decode.
//
// That decode is the repo's most protocol-coupled spot and it fails silently:
// rpc.go unmarshals the RPC's JSON-format AccountEntry into a struct that
// ignores unknown keys, so a reshaped AccountEntry yields zero-valued fields
// with no error at all. Asserting on populated fields is what turns such a
// change into a test failure instead of a silent data loss.
func (s *LedgerKeyAccountsTestSuite) TestGetLedgerKeyAccountsDecodesGenesisRootAccount() {
	t := s.T()

	body := fmt.Sprintf(`{"public_keys": [%q]}`, s.rootAddress)
	resp, err := http.Post(
		fmt.Sprintf("%s/api/v1/ledger-key/accounts?network=PUBLIC", s.connectionString),
		"application/json",
		strings.NewReader(body),
	)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Handlers wrap their payload in HttpResponse{Data any `json:"data"`}, so
	// the envelope has to be unwrapped with a concretely-typed Data field.
	var envelope struct {
		Data handlers.LedgerKeyAccountsResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope), "body: %s", raw)
	require.Empty(t, envelope.Data.Error.ErrorMessage, "handler reported an error fetching ledger entries")

	account, ok := envelope.Data.LedgerKeyAccounts[s.rootAddress]
	require.True(t, ok, "genesis root account %s missing from response; body: %s", s.rootAddress, raw)

	require.Equal(t, s.rootAddress, account.AccountId)

	// Core assigns rootAccount.balance = genesisLedger.totalCoins, so the
	// genesis root holds every lumen in existence — a zero or absent balance
	// means the decode dropped the field.
	require.NotEmpty(t, account.Balance)
	require.NotEqual(t, "0", account.Balance)

	// Deliberately no assertion on Seq_num: Core never assigns seqNum on the
	// genesis root entry (it sets only accountID, thresholds[0] and balance),
	// so zero is correct there. Confirmed against Core 28, which returns
	// seq_num "0" for this account — asserting non-zero would fail wrongly.
}
