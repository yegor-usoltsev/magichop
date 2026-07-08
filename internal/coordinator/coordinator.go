package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	server "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/yegor-usoltsev/magichop/internal/protocol"
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
	fmt.Fprintf(os.Stderr, "magichop server starting host=%s port=%d\n", opts.Host, opts.Port)
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

	url := fmt.Sprintf("nats://%s:%d", opts.Host, opts.Port)
	if opts.Host == "0.0.0.0" || opts.Host == "" {
		url = fmt.Sprintf("nats://127.0.0.1:%d", opts.Port)
	}
	nc, err := nats.Connect(url)
	if err != nil {
		ns.Shutdown()
		return err
	}
	defer nc.Close()

	core := NewCore(opts.AuthToken, func(ctx context.Context, peer string, req protocol.Release) (protocol.ReleaseReply, error) {
		data, err := json.Marshal(req)
		if err != nil {
			return protocol.ReleaseReply{}, err
		}
		msg, err := nc.RequestWithContext(ctx, SubjectRelease(peer), data)
		if err != nil {
			return protocol.ReleaseReply{}, err
		}
		var reply protocol.ReleaseReply
		if err := json.Unmarshal(msg.Data, &reply); err != nil {
			return protocol.ReleaseReply{}, err
		}
		return reply, nil
	})
	if err := subscribeHandlers(nc, core); err != nil {
		nc.Close()
		ns.Shutdown()
		return err
	}
	fmt.Fprintf(os.Stderr, "magichop server ready host=%s port=%d\n", opts.Host, opts.Port)
	<-ctx.Done()
	ns.Shutdown()
	if ctx.Err() == context.Canceled {
		fmt.Fprintln(os.Stderr, "magichop server stopped")
		return nil
	}
	return ctx.Err()
}

func subscribeHandlers(nc *nats.Conn, core *Core) error {
	if _, err := nc.Subscribe(SubjectRegister, func(msg *nats.Msg) {
		var req protocol.Register
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			respondJSON(msg, protocol.RegisterResult{Protocol: protocol.Version, Type: protocol.TypeRegisterResult, Status: protocol.ErrInvalidRequest, Reason: protocol.ErrInvalidRequest})
			return
		}
		respondJSON(msg, core.Register(req))
	}); err != nil {
		return err
	}
	if _, err := nc.Subscribe("mh.v1.heartbeat.*", func(msg *nats.Msg) {
		var req protocol.Heartbeat
		if err := json.Unmarshal(msg.Data, &req); err == nil {
			_ = core.Heartbeat(req)
		}
	}); err != nil {
		return err
	}
	if _, err := nc.Subscribe("mh.v1.claim.*", func(msg *nats.Msg) {
		if !strings.HasPrefix(msg.Subject, "mh.v1.claim.") {
			return
		}
		var req protocol.Claim
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			respondJSON(msg, protocol.ClaimResult{Protocol: protocol.Version, Type: protocol.TypeClaimResult, Status: protocol.ErrInvalidRequest, Reason: protocol.ErrInvalidRequest})
			return
		}
		respondJSON(msg, core.HandleClaim(context.Background(), req))
	}); err != nil {
		return err
	}
	nc.Flush()
	return nc.LastError()
}

func respondJSON(msg *nats.Msg, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = msg.Respond(data)
}
