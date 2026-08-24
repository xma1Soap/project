package wizard

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeConfigRejectsTrailingJSON(t *testing.T) {
	request := httptest.NewRequest("POST", "/api/save", strings.NewReader(`{"version":1} {}`))
	recorder := httptest.NewRecorder()
	if _, err := decodeConfig(recorder, request); err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}

func TestWizardRejectsNonLoopbackListener(t *testing.T) {
	err := (Server{Listen: "0.0.0.0:8765", Output: "/tmp/config.json"}).Serve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected loopback-only listener error, got %v", err)
	}
}
