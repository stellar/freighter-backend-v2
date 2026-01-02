package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/stellar/go-stellar-sdk/clients/rpcclient"
	rpc "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const (
	serviceName = "rpc"
)

type rpcService struct {
	pubnetClient    *rpcclient.Client
	testnetClient   *rpcclient.Client
	futurenetClient *rpcclient.Client
	httpClient      *http.Client
}

func createDefaultClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second, // Overall request timeout
		Transport: &http.Transport{
			// Connection pooling settings
			MaxIdleConns:        100,              // Total idle connections across all hosts
			MaxIdleConnsPerHost: 10,               // Idle connections per host
			MaxConnsPerHost:     50,               // Total connections per host (active + idle)
			IdleConnTimeout:     90 * time.Second, // How long idle connections stay alive

			// Timeout settings to prevent hung connections
			ResponseHeaderTimeout: 10 * time.Second, // Timeout waiting for response headers
			ExpectContinueTimeout: 1 * time.Second,  // Timeout for 100-continue

			// Connection settings
			DisableKeepAlives:  false, // Keep connections alive for reuse
			DisableCompression: false, // Allow compression
			ForceAttemptHTTP2:  true,  // Try HTTP/2
		},
	}
}

func NewRPCService(rpcURL string, testnetRPCURL string, futurenetRPCURL string) types.RPCService {
	httpClient := createDefaultClient()

	return &rpcService{
		httpClient:      httpClient,
		pubnetClient:    rpcclient.NewClient(rpcURL, httpClient),
		testnetClient:   rpcclient.NewClient(testnetRPCURL, httpClient),
		futurenetClient: rpcclient.NewClient(futurenetRPCURL, httpClient),
	}
}

func (r *rpcService) configureNetworkClient(network string) *rpcclient.Client {
	switch network {
	case types.TESTNET:
		return r.testnetClient
	case types.FUTURENET:
		return r.futurenetClient
	case types.PUBLIC:
		return r.pubnetClient
	}
	return r.pubnetClient
}

func (r *rpcService) Name() string {
	return serviceName
}

func (r *rpcService) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	networkclient := r.configureNetworkClient(network)
	response, err := networkclient.GetHealth(ctx)
	if err != nil {
		return types.GetHealthResponse{Status: types.StatusError}, err
	}

	return types.GetHealthResponse{
		Status: response.Status,
	}, nil
}

func (r *rpcService) SimulateTx(
	ctx context.Context,
	tx *txnbuild.Transaction,
	network string,
) (types.SimulateTransactionResponse, error) {
	txeB64, err := tx.Base64()
	if err != nil {
		return nil, fmt.Errorf("could not encode transaction: %w", err)
	}

	networkclient := r.configureNetworkClient(network)
	resp, err := networkclient.SimulateTransaction(ctx, rpc.SimulateTransactionRequest{
		Transaction: txeB64,
	})
	if err != nil {
		return nil, fmt.Errorf("simulateTransaction RPC failed: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("simulateTransaction returned error: %s", resp.Error)
	}
	if len(resp.Results) == 0 {
		return nil, fmt.Errorf("no results in simulation")
	}

	if resp.Results[0].ReturnValueXDR == nil {
		return nil, fmt.Errorf("simulateTransaction result has nil ReturnValueXDR")
	}

	var retval xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(*resp.Results[0].ReturnValueXDR, &retval); err != nil {
		return nil, fmt.Errorf("failed to decode result XDR: %w", err)
	}

	return &retval, nil
}

func (r *rpcService) SimulateInvocation(
	ctx context.Context,
	contractId xdr.ScAddress,
	sourceAccount *txnbuild.SimpleAccount,
	functionName xdr.ScSymbol,
	params []xdr.ScVal,
	timeout txnbuild.TimeBounds,
	network string,
) (types.SimulateTransactionResponse, error) {
	contractHash := contractId.ContractId
	contractIdStr, err := strkey.Encode(strkey.VersionByteContract, contractHash[:])
	if err != nil || !utils.IsValidContractID(contractIdStr) {
		return nil, fmt.Errorf("invalid contract ID: %w", err)
	}

	invokeOp := txnbuild.InvokeHostFunction{
		HostFunction: xdr.HostFunction{
			Type: xdr.HostFunctionType(0),
			InvokeContract: &xdr.InvokeContractArgs{
				ContractAddress: contractId,
				FunctionName:    functionName,
				Args:            params,
			},
		},
	}

	txParams := txnbuild.TransactionParams{
		SourceAccount:        sourceAccount,
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{&invokeOp},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions: txnbuild.Preconditions{
			TimeBounds: timeout,
		},
	}

	tx, err := txnbuild.NewTransaction(txParams)
	if err != nil {
		return nil, fmt.Errorf("failed to build transaction: %w", err)
	}

	return r.SimulateTx(ctx, tx, network)
}

func (r *rpcService) GetLedgerEntries(ctx context.Context, keys []string, network string) ([]types.LedgerEntryMap, error) {
	networkClient := r.configureNetworkClient(network)
	response, err := networkClient.GetLedgerEntries(ctx, rpc.GetLedgerEntriesRequest{
		Keys:   keys,
		Format: "json",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get ledger entries: %w", err)
	}

	var entries []types.LedgerEntryMap

	for _, entry := range response.Entries {
		var entryMap types.LedgerEntryMap
		if unmarshalErr := json.Unmarshal(entry.DataJSON, &entryMap); unmarshalErr != nil {
			return nil, fmt.Errorf("failed to unmarshal ledger entry: %w", unmarshalErr)
		}
		entries = append(entries, entryMap)
	}

	return entries, nil
}
