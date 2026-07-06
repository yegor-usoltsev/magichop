package bluetooth

import (
	"context"
	"sync"
	"time"
)

type Fake struct {
	mu             sync.Mutex
	Connected      []string
	Disconnected   []string
	FailDisconnect bool
}

func (f *Fake) Connect(_ context.Context, address string, _ time.Duration) Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Connected = append(f.Connected, address)
	return Result{OK: true}
}

func (f *Fake) Disconnect(_ context.Context, address string, _ time.Duration) Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Disconnected = append(f.Disconnected, address)
	if f.FailDisconnect {
		return Result{OK: false, Error: "disconnect failed"}
	}
	return Result{OK: true}
}

func (f *Fake) IsConnected(_ context.Context, _ string, _ time.Duration) (bool, Result) {
	return false, Result{OK: true, Stdout: "0"}
}
