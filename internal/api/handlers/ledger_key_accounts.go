package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/go/xdr"
)


type LedgerKeyAccountHandler struct {
	RpcService types.RPCService

}

type LedgerKeyAccountMap map[string]types.AccountInfo


func NewLedgerKeyAccountHandler(rpc types.RPCService) *LedgerKeyAccountHandler {
	return &LedgerKeyAccountHandler{
		RpcService: rpc,
	}
}

type PublicKeyError struct {
	PublicKey    string `json:"public_key"`
	ErrorMessage string `json:"error_message"`
}

type LedgerKeyAccountError struct {
	ErrorMessage      string       `json:"error_message"`
	ErrorKeys        []PublicKeyError `json:"error_keys,omitempty"`
}

type LedgerKeyAccountsResponse struct {
	LedgerKeyAccounts map[string]types.AccountInfo `json:"ledger_key_accounts"` // Removed omitempty
	Error       LedgerKeyAccountError `json:"error,omitempty"`
}

type LedgerKeyAccountKeys struct {
	LedgerKeys []string `json:"ledger_keys"`
	LedgerKeyAccountMap LedgerKeyAccountMap `json:"ledger_key_account_map"`
}

func getLedgerKeyAccountKeys(publicKeys []string) (LedgerKeyAccountKeys, LedgerKeyAccountError) {
	ledgerKeyAccountMap := LedgerKeyAccountMap{}
	ledgerKeyAccountError := LedgerKeyAccountError{ErrorKeys: []PublicKeyError{}}
	ledgerKeyAccountKeys := []string{}

	for _, publicKey := range publicKeys {
		accountId, err := xdr.AddressToAccountId(publicKey)
		if err != nil {
			ledgerKeyAccountError.ErrorMessage = "error converting public key to account id"
			ledgerKeyAccountError.ErrorKeys = append(ledgerKeyAccountError.ErrorKeys, PublicKeyError{PublicKey: publicKey, ErrorMessage: err.Error()})
		} else {
			key := xdr.LedgerKey{}
	
			err = key.SetAccount(accountId)
			if err != nil {
				ledgerKeyAccountError.ErrorMessage = "error setting account id"
				ledgerKeyAccountError.ErrorKeys = append(ledgerKeyAccountError.ErrorKeys, PublicKeyError{PublicKey: publicKey, ErrorMessage: err.Error()})
			} else {
				ledgerKeyAccount := xdr.LedgerKeyAccount{
					AccountId: accountId,
				}
			
				ledgerKey := xdr.LedgerKey{
					Type: xdr.LedgerEntryTypeAccount,
					Account: &ledgerKeyAccount,
				}
			
			
				bkey, err := ledgerKey.MarshalBinary()
				if err != nil {
					ledgerKeyAccountError.ErrorMessage = "error marshalling ledger key"
					ledgerKeyAccountError.ErrorKeys = append(ledgerKeyAccountError.ErrorKeys, PublicKeyError{PublicKey: publicKey, ErrorMessage: err.Error()})
				}
			
			
				xdr := base64.StdEncoding.EncodeToString(bkey)
				ledgerKeyAccountKeys = append(ledgerKeyAccountKeys, xdr)
				ledgerKeyAccountMap[publicKey] = types.AccountInfo{}
			}
		}
	}

	return LedgerKeyAccountKeys{LedgerKeys: ledgerKeyAccountKeys, LedgerKeyAccountMap: ledgerKeyAccountMap}, ledgerKeyAccountError
}

func processLedgerKeyAccountsEntries(publicKeys []string, data []types.LedgerEntryMap) (LedgerKeyAccountMap, LedgerKeyAccountError) {
	ledgerKeyAccountsMap := LedgerKeyAccountMap{}
	ledgerKeyAccountsError := LedgerKeyAccountError{ErrorKeys: []PublicKeyError{}}

	for _, publicKey := range publicKeys {
		for _, entry := range data {
			if (entry.Account.AccountId == publicKey) {
				ledgerKeyAccountsMap[publicKey] = entry.Account
				break
			}
		} 
	}

	return ledgerKeyAccountsMap, ledgerKeyAccountsError
}

// GetLedgerKeyAccounts handles creating ledger keys from public keys and then fetches the account info from the RPC service
// It returns a map of public keys to AccountInfo and a map of errors if some of the public keys are invalid
// This is designed to be flexible so valid public keys will return results while invalid public keys will return errors
func (h *LedgerKeyAccountHandler) GetLedgerKeyAccounts(w http.ResponseWriter, r *http.Request) error {
	contextWithTimeout, cancel := context.WithTimeout(r.Context(), HealthCheckContextTimeout)
	defer cancel()
	ledgerKeyAccountList := make(map[string]types.AccountInfo)
	var ledgerKeyAccountError LedgerKeyAccountError
	queryParams := r.URL.Query()
	network := queryParams.Get("network") 

	if (len(queryParams) == 0) {
		return httperror.BadRequest("no params passed: public key and network query params are required", errors.New("no params provided"))
	}

	if network != types.PUBLIC && network != types.TESTNET && network != types.FUTURENET {
		return httperror.BadRequest(fmt.Sprintf("invalid network: network must be %s, %s or %s", types.PUBLIC, types.TESTNET, types.FUTURENET), errors.New("invalid network"))
	}

	for key, publicKeys := range queryParams {
		if key == "public_key" {
			deduplicatedPublicKeys := []string{}
			
			for _, publicKey := range publicKeys {
				// RPC does not tolerate duplicate public keys, so we need to remove duplicates
				if !slices.Contains(deduplicatedPublicKeys, publicKey) {
					deduplicatedPublicKeys = append(deduplicatedPublicKeys, publicKey)
				}
			}

			ledgerKeyAccountKeys, ledgerKeyAccountKeysError := getLedgerKeyAccountKeys(deduplicatedPublicKeys)
			if ledgerKeyAccountKeysError.ErrorMessage != "" {
				ledgerKeyAccountError = ledgerKeyAccountKeysError
			}
			ledgerKeyAccountList = ledgerKeyAccountKeys.LedgerKeyAccountMap
			
			ledgerKeyAccountsRpcData, e := FetchLedgerEntries(h.RpcService, contextWithTimeout, ledgerKeyAccountKeys.LedgerKeys, network)

			if e != nil && ledgerKeyAccountKeysError.ErrorMessage == "" {
				ledgerKeyAccountError = LedgerKeyAccountError{ErrorMessage: e.Error()}
			}

			processedLedgerKeyAccountsMap, processedLedgerKeyAccountsError := processLedgerKeyAccountsEntries(deduplicatedPublicKeys, ledgerKeyAccountsRpcData)
			if processedLedgerKeyAccountsError.ErrorMessage != "" {
				ledgerKeyAccountError = processedLedgerKeyAccountsError
			}
			maps.Copy(ledgerKeyAccountList, processedLedgerKeyAccountsMap)
		}
	}

	if (len(ledgerKeyAccountList) == 0 && ledgerKeyAccountError.ErrorMessage != "") {
		return httperror.BadRequest(fmt.Sprintf("%s: %s", "No entries found", ledgerKeyAccountError.ErrorMessage),
		 errors.New(ledgerKeyAccountError.ErrorMessage))
	}



	resp := LedgerKeyAccountsResponse{
		LedgerKeyAccounts: ledgerKeyAccountList,
		Error:       ledgerKeyAccountError,
	}

	responseData := HttpResponse{
		Data: LedgerKeyAccountsResponse{
			LedgerKeyAccounts: resp.LedgerKeyAccounts,
			Error:       resp.Error,
		},
	}

	w.Header().Set("Content-Type", "application/json")

	return response.OK(w, responseData)
}
