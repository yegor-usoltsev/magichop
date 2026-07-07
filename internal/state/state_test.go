package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendAndRecentFinalResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.jsonl")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(Event{Event: EventAccepted, RequestID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(Event{Event: EventFinalResult, RequestID: "a", OK: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := RecentFinalResults(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestID != "a" || !got[0].OK {
		t.Fatalf("unexpected recent results: %+v", got)
	}
}

func TestRecentFinalResultsLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.jsonl")
	data := []byte(`{"event":"final_result","request_id":"a"}
{"event":"final_result","request_id":"b"}
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := RecentFinalResults(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestID != "b" {
		t.Fatalf("unexpected recent results: %+v", got)
	}
}

func TestTerminalizeOpenAcceptedClaims(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.jsonl")
	data := []byte(`{"event":"accepted","request_id":"a","client_request_id":"c"}
{"event":"accepted","request_id":"b","client_request_id":"d"}
{"event":"final_result","request_id":"b","error":"done"}
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := TerminalizeOpen(path, "daemon_restarted"); err != nil {
		t.Fatal(err)
	}
	got, err := RecentFinalResults(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("final results = %d, want 2: %+v", len(got), got)
	}
	if got[1].RequestID != "a" || got[1].Error != "daemon_restarted" {
		t.Fatalf("unexpected terminalized event: %+v", got[1])
	}
}
