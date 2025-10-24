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


type HomeDomainsHandler struct {
	RpcService types.RPCService

}

type HomeDomainMap map[string]string


func NewHomeDomainsHandler(rpc types.RPCService) *HomeDomainsHandler {
	return &HomeDomainsHandler{
		RpcService: rpc,
	}
}

type PublicKeyError struct {
	PublicKey    string `json:"public_key"`
	ErrorMessage string `json:"error_message"`
}

type HomeDomainsError struct {
	ErrorMessage      string       `json:"error_message"`
	Error_keys        []PublicKeyError `json:"error_keys,omitempty"`
}

type HomeDomainsResponse struct {
	HomeDomains map[string]string `json:"home_domains"` // Removed omitempty
	Error       HomeDomainsError `json:"error,omitempty"`
}

type LedgerKeyAccountKeys struct {
	LedgerKeys []string `json:"ledger_keys"`
	HomeDomainMap HomeDomainMap `json:"home_domain_map"`
}

func getLedgerKeyAccountKeys(publicKeys []string) (LedgerKeyAccountKeys, HomeDomainsError) {
	homeDomainMap := HomeDomainMap{}
	homeDomainsError := HomeDomainsError{Error_keys: []PublicKeyError{}}
	ledgerKeyAccountKeys := []string{}

	for _, publicKey := range publicKeys {
		accountId, err := xdr.AddressToAccountId(publicKey)
		if err != nil {
			homeDomainsError.ErrorMessage = "error converting public key to account id"
			homeDomainsError.Error_keys = append(homeDomainsError.Error_keys, PublicKeyError{PublicKey: publicKey, ErrorMessage: err.Error()})
		} else {
			key := xdr.LedgerKey{}
	
			err = key.SetAccount(accountId)
			if err != nil {
				homeDomainsError.ErrorMessage = "error setting account id"
				homeDomainsError.Error_keys = append(homeDomainsError.Error_keys, PublicKeyError{PublicKey: publicKey, ErrorMessage: err.Error()})
			}
		
			ledgerKeyAccount := xdr.LedgerKeyAccount{
				AccountId: accountId,
			}
		
			ledgerKey := xdr.LedgerKey{
				Type: xdr.LedgerEntryTypeAccount,
				Account: &ledgerKeyAccount,
			}
		
		
			bkey, err := ledgerKey.MarshalBinary()
			if err != nil {
				homeDomainsError.ErrorMessage = "error marshalling ledger key"
				homeDomainsError.Error_keys = append(homeDomainsError.Error_keys, PublicKeyError{PublicKey: publicKey, ErrorMessage: err.Error()})
			}
		
		
		
			xdr := base64.StdEncoding.EncodeToString(bkey)
			ledgerKeyAccountKeys = append(ledgerKeyAccountKeys, xdr)
			homeDomainMap[publicKey] = ""
		}
	}

	return LedgerKeyAccountKeys{LedgerKeys: ledgerKeyAccountKeys, HomeDomainMap: homeDomainMap}, homeDomainsError
}

func processHomeDomainLedgerEntries(publicKeys []string, data []types.LedgerEntryMap) (HomeDomainMap, HomeDomainsError) {
	homeDomainMap := HomeDomainMap{}
	homeDomainsError := HomeDomainsError{Error_keys: []PublicKeyError{}}

	for _, publicKey := range publicKeys {
		for _, entry := range data {
			if (entry.Account.AccountId == publicKey) {
				homeDomainMap[publicKey] = entry.Account.HomeDomain
				break
			}
		} 
	}

	return homeDomainMap, homeDomainsError
}

// GetHomeDomains handles created ledger keys from public keys and then fetches home domains from the RPC service
// It returns a map of public keys to home domains and a map of errors if some of the public keys are invalid
// This is designed to be flexible so valid public keys will return results while invalid public keys will return errors
func (h *HomeDomainsHandler) GetHomeDomains(w http.ResponseWriter, r *http.Request) error {
	_, cancel := context.WithTimeout(r.Context(), HealthCheckContextTimeout)
	defer cancel()
	homeDomainList := make(map[string]string)
	var homeDomainsError HomeDomainsError
	queryParams := r.URL.Query()
	network := queryParams.Get("network") 

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
				homeDomainsError = ledgerKeyAccountKeysError
			}
			homeDomainList = ledgerKeyAccountKeys.HomeDomainMap
			
			homeDomainRpcData, e := FetchHomeDomains(h.RpcService, r.Context(), ledgerKeyAccountKeys.LedgerKeys, network)

			if e != nil {
				homeDomainsError = HomeDomainsError{ErrorMessage: e.Error()}
			}

			processedHomeDomainMap, processedHomeDomainsError := processHomeDomainLedgerEntries(deduplicatedPublicKeys, homeDomainRpcData)
			if processedHomeDomainsError.ErrorMessage != "" {
				homeDomainsError = processedHomeDomainsError
			}
			maps.Copy(homeDomainList, processedHomeDomainMap)
		}
	}

	if (len(homeDomainList) == 0 && homeDomainsError.ErrorMessage != "") {
		return httperror.BadRequest(fmt.Sprintf("%s: %s", "No entries found", homeDomainsError.ErrorMessage),
		 errors.New(homeDomainsError.ErrorMessage))
	}



	resp := HomeDomainsResponse{
		HomeDomains: homeDomainList,
		Error:       homeDomainsError,
	}

	responseData := HttpResponse{
		Data: HomeDomainsResponse{
			HomeDomains: resp.HomeDomains,
			Error:       resp.Error,
		},
	}

	w.Header().Set("Content-Type", "application/json")

	return response.OK(w, responseData)
}
