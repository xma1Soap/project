package newapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSnapshotUsesRootHeadersAndDecodesSanitizedBulkData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/channel/quota-snapshot" || r.URL.Query().Get("ids") != "2,9" {
			http.Error(w, "unexpected path", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "test-token" || r.Header.Get("New-Api-User") != "7" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"message":"","data":{"generated_at":1,"channels":[{"channel_id":2,"status":1,"used_quota":15,"tag":"managed"},{"channel_id":9,"status":1,"used_quota":20,"tag":"managed"}],"telemetry":[{"channel_id":2,"model":"m","successes":3,"hard_quota_errors":1,"rate_limit_errors":2,"other_errors":0,"consecutive_hard":1,"last_event_unix":1,"last_kind":"hard_quota"}]}}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, 7, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Snapshot(context.Background(), []int{9, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Channels) != 2 || len(snapshot.Telemetry) != 1 || snapshot.Channels[0].UsedQuota != 15 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestClientRejectsRedirectsAndOversizedResponses(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.invalid/", http.StatusFound)
	}))
	defer redirect.Close()
	client, err := NewClient(redirect.URL, 1, "token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Snapshot(context.Background(), []int{1}); err == nil {
		t.Fatal("expected redirect response to be rejected")
	}

	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
	}))
	defer oversized.Close()
	client, err = NewClient(oversized.URL, 1, "token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Snapshot(context.Background(), []int{1}); err == nil {
		t.Fatal("expected oversized response to be rejected")
	}
}
