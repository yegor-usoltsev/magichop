package state

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	EventAccepted     = "accepted"
	EventFinalResult  = "final_result"
	EventLocalRelease = "local_release"
	EventGuard        = "post_success_guard"
)

type Event struct {
	Event           string    `json:"event"`
	TS              time.Time `json:"ts"`
	RequestID       string    `json:"request_id,omitempty"`
	ClientRequestID string    `json:"client_request_id,omitempty"`
	Device          string    `json:"device,omitempty"`
	Address         string    `json:"address,omitempty"`
	OK              bool      `json:"ok,omitempty"`
	Error           string    `json:"error,omitempty"`
	DurationMS      int64     `json:"duration_ms,omitempty"`
	Until           time.Time `json:"until,omitempty"`
}

type Store struct {
	file *os.File
}

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("Library", "Application Support", "MagicHop", "state.jsonl")
	}
	return filepath.Join(home, "Library", "Application Support", "MagicHop", "state.jsonl")
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Store{file: f}, nil
}

func (s *Store) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	return s.file.Close()
}

func (s *Store) Append(event Event) error {
	if s == nil || s.file == nil {
		return errors.New("state store closed")
	}
	if event.TS.IsZero() {
		event.TS = time.Now().UTC()
	}
	if err := json.NewEncoder(s.file).Encode(event); err != nil {
		return err
	}
	return s.file.Sync()
}

func RecentFinalResults(path string, limit int) ([]Event, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return RecentFinalResultsFromReader(f, limit)
}

func TerminalizeOpen(path string, reason string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(f)
	accepted := map[string]Event{}
	final := map[string]bool{}
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			_ = f.Close()
			return err
		}
		switch event.Event {
		case EventAccepted:
			accepted[event.RequestID] = event
		case EventFinalResult:
			final[event.RequestID] = true
		}
	}
	if err := scanner.Err(); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()
	store, err := Open(path)
	if err != nil {
		return err
	}
	defer store.Close()
	for requestID, event := range accepted {
		if final[requestID] {
			continue
		}
		if err := store.Append(Event{Event: EventFinalResult, RequestID: requestID, ClientRequestID: event.ClientRequestID, OK: false, Error: reason}); err != nil {
			return err
		}
	}
	return nil
}

func RecentFinalResultsFromReader(r io.Reader, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	var recent []Event
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, err
		}
		if event.Event != EventFinalResult {
			continue
		}
		recent = append(recent, event)
		if len(recent) > limit {
			recent = recent[len(recent)-limit:]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return recent, nil
}
