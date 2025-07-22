package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	response "github.com/stellar/freighter-backend-v2/internal/api/httpresponse"
	"github.com/stellar/freighter-backend-v2/internal/logger"
)

var (
	ErrFailedToReadProtocolsConfig = httperror.ErrorMessage{
		LogMessage:    "failed to read protocols config from %s: %v",
		ClientMessage: "An error occurred while fetching protocol configurations.",
	}
	ErrFailedToUnmarshalProtocolsConfig = httperror.ErrorMessage{
		LogMessage:    "failed to unmarshal protocols config: %v. Data (first %d bytes): %s",
		ClientMessage: "An error occurred while processing protocol configurations.",
	}
	ErrFailedToEncodeProtocolsToJSONResponse = httperror.ErrorMessage{
		LogMessage:    "failed to encode protocols to JSON response: %v",
		ClientMessage: "An error occurred while formatting the response.",
	}
)

type Protocol struct {
	Name                        string   `json:"name"`
	Tags                        []string `json:"tags"`
	URL                         string   `json:"website_url"`
	IconURL                     string   `json:"icon_url"`
	Description                 string   `json:"description"`
	IsBlacklisted               bool     `json:"is_blacklisted"`
	IsWalletConnectNotSupported bool     `json:"is_wc_not_supported"`
}

// ProtocolsPayload encapsulates the list of protocols under a specific key.
// This is used to structure the response under a "data" field.
type GetProtocolsPayload struct {
	Protocols []Protocol `json:"protocols"`
}

// ProtocolHandler holds dependencies for protocol-related handlers.
type ProtocolsHandler struct {
	protocolsConfigPath string
}

// NewProtocolHandler creates a new ProtocolHandler instance.
func NewProtocolsHandler(protocolsConfigPath string) *ProtocolsHandler {
	return &ProtocolsHandler{
		protocolsConfigPath: protocolsConfigPath,
	}
}

// GetProtocols handles requests to fetch the list of supported protocols.
// It reads the protocol information based on the configured path.
func (h *ProtocolsHandler) GetProtocols(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	data, err := os.ReadFile(h.protocolsConfigPath)
	if err != nil {
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrFailedToReadProtocolsConfig.LogMessage, h.protocolsConfigPath, err))
		if os.IsNotExist(err) {
			return httperror.NotFound(ErrFailedToReadProtocolsConfig.ClientMessage, err)
		}
		return httperror.InternalServerError(ErrFailedToReadProtocolsConfig.ClientMessage, err)
	}

	var protocols []Protocol
	err = json.Unmarshal(data, &protocols)
	if err != nil {
		snippetLength := 100
		if len(data) < snippetLength {
			snippetLength = len(data)
		}
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrFailedToUnmarshalProtocolsConfig.LogMessage, err, snippetLength, string(data[:snippetLength])))
		return httperror.InternalServerError(ErrFailedToUnmarshalProtocolsConfig.ClientMessage, err)
	}

	responseData := HttpResponse{
		Data: GetProtocolsPayload{
			Protocols: protocols,
		},
	}

	if err := response.OK(w, responseData); err != nil {
		logger.ErrorWithContext(ctx, fmt.Sprintf(ErrFailedToEncodeProtocolsToJSONResponse.LogMessage, err))
		return httperror.InternalServerError(ErrFailedToEncodeProtocolsToJSONResponse.ClientMessage, err)
	}
	return nil
}
