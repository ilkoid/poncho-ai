package wb

import (
	"errors"
	"fmt"
	"testing"
)

// Real-shaped error observed from supplies-api /api/v1/warehouses since 2026-08-15.
const disabledBody = `{"status":404,"statusText":"Not Found","title":"Not Found","detail":"This method is temporarily disabled. Link: https://dev.wildberries.ru/release-notes?id=570","requestId":"ca265cdc70d8379852b583d4d140610b","origin":"ag-supplies"}`

func TestIsTemporarilyDisabled(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"raw 404 disabled", fmt.Errorf("wb api error: status 404, body: %s", disabledBody), true},
		{"wrapped 404 disabled", fmt.Errorf("get warehouses: %w", fmt.Errorf("wb api error: status 404, body: %s", disabledBody)), true},
		{"404 without marker", fmt.Errorf("wb api error: status 404, body: {\"detail\":\"Not Found\"}"), false},
		{"500 with marker text", fmt.Errorf("wb api error: status 500, body: temporarily disabled"), false},
		{"network error", errors.New("dial tcp: connection refused"), false},
	}
	for _, tt := range tests {
		if got := IsTemporarilyDisabled(tt.err); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
