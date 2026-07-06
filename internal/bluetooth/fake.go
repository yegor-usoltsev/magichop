package bluetooth

import (
	"context"
	"sync"
	"time"
)

type Fake struct {
	mu              sync.Mutex
	Connected       []string
	ConnectTimeouts []time.Duration
	Released        []string
	FailConnects    int
	FailRelease     bool
}

func (f *Fake) Connect(_ context.Context, address string, timeout time.Duration) Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Connected = append(f.Connected, address)
	f.ConnectTimeouts = append(f.ConnectTimeouts, timeout)
	if f.FailConnects > 0 {
		f.FailConnects--
		return Result{OK: false, Error: "connect failed"}
	}
	return Result{OK: true}
}

func (f *Fake) Release(_ context.Context, address string, _ time.Duration) Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Released = append(f.Released, address)
	if f.FailRelease {
		return Result{OK: false, Error: "release failed"}
	}
	return Result{OK: true}
}

func (f *Fake) IsConnected(_ context.Context, _ string, _ time.Duration) (bool, Result) {
	return false, Result{OK: true, Stdout: "0"}
}
