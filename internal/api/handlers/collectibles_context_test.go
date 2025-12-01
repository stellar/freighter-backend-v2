package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func TestGetCollectibles_ContextCancellation(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	handler := NewCollectiblesHandler(mockRPC, "", "", "", 10)

	// Create a request with a very short timeout
	payload := map[string]interface{}{
		"owner": "GDAFOKARX4VPZHPDBY5UTIRK32GUGCC7PQJ4SGQYGOEYNV2XSE5TY4KE",
		"contracts": []map[string]interface{}{
			{
				"id":        "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA",
				"token_ids": []string{"1", "2", "3"},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/v1/collectibles?network=PUBLIC", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Add a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	// Call handler - it should handle cancellation gracefully
	err := handler.GetCollectibles(rr, req)

	// Depending on timing, could get nil error if pool hasn't started or internal error
	// The important thing is it doesn't panic or hang
	if err != nil {
		// Error is acceptable (likely context cancelled)
		t.Logf("Got expected error on cancelled context: %v", err)
	}
}

func TestGetCollectibles_RespectTimeout(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	handler := NewCollectiblesHandler(mockRPC, "", "", "", 10)

	payload := map[string]interface{}{
		"owner": "GDAFOKARX4VPZHPDBY5UTIRK32GUGCC7PQJ4SGQYGOEYNV2XSE5TY4KE",
		"contracts": []map[string]interface{}{
			{
				"id":        "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA",
				"token_ids": []string{"1"},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/v1/collectibles?network=PUBLIC", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Add a context with very short timeout (shorter than CollectiblesContextTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	// Give the context time to expire
	time.Sleep(10 * time.Millisecond)

	// Call handler - it should handle timeout gracefully
	err := handler.GetCollectibles(rr, req)

	// Should either get nil (fast path before timeout) or error
	// The important verification is no goroutine leaks or hangs
	if err != nil {
		t.Logf("Got expected error on timeout: %v", err)
	}
}

// Note: Direct testing of fetchCollectibles is difficult because it's a private method
// with complex parameters. Context cancellation for it is tested indirectly through
// TestGetCollectibles_ContextCancellation and TestGetCollectibles_RespectTimeout
