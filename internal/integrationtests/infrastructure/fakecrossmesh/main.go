// ABOUTME: Fake Cross Mesh Ingress API for integration tests: chains, deposit addresses, deposits and settlements.
// ABOUTME: ENV picks the chain ids, SCENARIO=all_suspended suspends every chain; deposit histories are chosen by recipient.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/services"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const listenAddr = ":8080"

// depositAddress is a deposit address row as the API returns it.
type depositAddress struct {
	DepositAddress string `json:"deposit_address"`
	Recipient      string `json:"recipient"`
	DeliveryAsset  string `json:"delivery_asset"`
	Speed          string `json:"speed"`
	Index          uint64 `json:"index"`
	CreatedAt      string `json:"created_at"`
}

// server holds the deposit addresses created so far, one per recipient.
type server struct {
	chains    []map[string]any
	mu        sync.Mutex
	addresses map[string]depositAddress
}

func main() {
	s := &server{
		chains:    chains(os.Getenv("ENV"), os.Getenv("SCENARIO")),
		addresses: map[string]depositAddress{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/chains", s.getChains)
	mux.HandleFunc("POST /v1/deposit-addresses", s.createDepositAddress)
	mux.HandleFunc("GET /v1/deposit-addresses", s.listDepositAddresses)
	mux.HandleFunc("GET /v1/deposits", listDeposits)
	mux.HandleFunc("GET /v1/settlements/{id}", getSettlement)

	srv := &http.Server{Addr: listenAddr, Handler: requireBearer(mux), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// requireBearer answers 401 authentication_failed unless the request carries
// a non-empty bearer key. Any key is accepted.
func requireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || key == "" {
			writeError(w, http.StatusUnauthorized, "authentication_failed", "missing or invalid API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) getChains(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"chains": s.chains})
}

// createDepositAddress is get-or-create keyed by recipient. A new address
// takes the next index, starting at 0, and is derived from the testnet
// contracts exactly as the factory does.
func (s *server) createDepositAddress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Recipient     string `json:"recipient"`
		DeliveryAsset string `json:"delivery_asset"`
		Speed         string `json:"speed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Recipient == "" || body.DeliveryAsset == "" || body.Speed == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "recipient, delivery_asset and speed are required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.addresses[body.Recipient]
	if !ok {
		env, _ := services.CrossMeshEnvFor(types.TESTNET)
		index := uint64(len(s.addresses))
		address, err := services.ComputeDepositAddress(body.Recipient, index, body.Speed == "fast", env.Factory, env.Implementation)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		row = depositAddress{
			DepositAddress: address, Recipient: body.Recipient, DeliveryAsset: body.DeliveryAsset,
			Speed: body.Speed, Index: index, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		s.addresses[body.Recipient] = row
	}
	writeJSON(w, http.StatusOK, row)
}

// listDepositAddresses returns the recipient's address when it matches the
// delivery_asset and speed filters, else an empty list.
func (s *server) listDepositAddresses(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	row, ok := s.addresses[q.Get("recipient")]
	s.mu.Unlock()

	data := []depositAddress{}
	if ok && row.DeliveryAsset == q.Get("delivery_asset") && row.Speed == q.Get("speed") {
		data = append(data, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "has_more": false, "next_cursor": nil})
}

// listDeposits pages through the recipient's canned deposits: at most limit
// rows after the deposit named by starting_after, in the canned order.
func listDeposits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request", "limit must be a positive integer")
		return
	}
	rows := depositsByRecipient[q.Get("recipient")]

	start := 0
	if after := q.Get("starting_after"); after != "" {
		for i, row := range rows {
			if row["deposit_id"] == after {
				start = i + 1
			}
		}
	}
	end := min(start+limit, len(rows))
	page := append([]map[string]any{}, rows[start:end]...)

	var next any
	if end < len(rows) {
		next = rows[end-1]["deposit_id"]
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": page, "has_more": next != nil, "next_cursor": next})
}

// getSettlement serves a canned settlement round; unavailableSettlementID
// answers 500 to stand in for an upstream outage.
func getSettlement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == unavailableSettlementID {
		writeError(w, http.StatusInternalServerError, "internal_error", "settlement store unavailable")
		return
	}
	settlement, ok := settlements[id]
	if !ok {
		writeError(w, http.StatusNotFound, "settlement_not_found", "no such settlement")
		return
	}
	writeJSON(w, http.StatusOK, settlement)
}

// writeError writes Cross Mesh's error envelope.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": "req_fake"}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("writing response: %v", err)
	}
}
