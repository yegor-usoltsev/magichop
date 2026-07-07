package coordinator

import (
	"context"
	"fmt"
	"time"

	server "github.com/nats-io/nats-server/v2/server"
)

type Options struct {
	Host      string
	Port      int
	AuthToken string
}

const (
	HeartbeatInterval = 61 * time.Second
	DeadPeerAfter     = 127 * time.Second
	NewClaimMinimum   = 7 * time.Second
	ReleaseStartWait  = 300 * time.Millisecond
	ReleaseWindow     = 2 * time.Second
)

func Run(ctx context.Context, opts Options) error {
	if opts.AuthToken == "" {
		return fmt.Errorf("auth token required")
	}
	ns, err := server.NewServer(&server.Options{
		Host:      opts.Host,
		Port:      opts.Port,
		NoLog:     true,
		NoSigs:    true,
		JetStream: false,
	})
	if err != nil {
		return err
	}
	ns.Start()
	if !ns.ReadyForConnections(10 * time.Second) {
		ns.Shutdown()
		return fmt.Errorf("nats server did not become ready")
	}
	<-ctx.Done()
	ns.Shutdown()
	return ctx.Err()
}
