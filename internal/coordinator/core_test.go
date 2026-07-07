package coordinator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yegor-usoltsev/magichop/internal/protocol"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func TestDeviceHashAndSubjects(t *testing.T) {
	if got := DeviceHash("aa:bb:cc:dd:ee:ff"); got != "c1582e87c8022218" {
		t.Fatalf("DeviceHash = %q", got)
	}
	if got := SubjectClaim("aa:bb:cc:dd:ee:ff"); got != "mh.v1.claim.c1582e87c8022218" {
		t.Fatalf("SubjectClaim = %q", got)
	}
	if got := SubjectRelease("macbook-b"); got != "mh.v1.release.macbook-b" {
		t.Fatalf("SubjectRelease = %q", got)
	}
}

func TestPeerSelectionMostRecentHeartbeatThenLexical(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	var selected string
	core := NewCore("token", func(_ context.Context, peer string, req protocol.Release) (protocol.ReleaseReply, error) {
		selected = peer
		return protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "started"}, nil
	})
	core.SetClock(clock)
	registerNode(core, "requester", "aa:bb:cc:dd:ee:ff")
	registerNode(core, "macbook-c", "aa:bb:cc:dd:ee:ff")
	clock.now = clock.now.Add(time.Second)
	registerNode(core, "macbook-b", "aa:bb:cc:dd:ee:ff")

	res := core.HandleClaim(context.Background(), claim("requester", "aa:bb:cc:dd:ee:ff", 11000))
	if res.Status != "proceed" {
		t.Fatalf("status = %q", res.Status)
	}
	if selected != "macbook-b" {
		t.Fatalf("selected peer = %q, want macbook-b", selected)
	}
}

func TestPeerSelectionLexicalTie(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	var selected string
	core := NewCore("token", func(_ context.Context, peer string, req protocol.Release) (protocol.ReleaseReply, error) {
		selected = peer
		return protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "started"}, nil
	})
	core.SetClock(clock)
	registerNode(core, "requester", "aa:bb:cc:dd:ee:ff")
	registerNode(core, "macbook-c", "aa:bb:cc:dd:ee:ff")
	registerNode(core, "macbook-b", "aa:bb:cc:dd:ee:ff")

	res := core.HandleClaim(context.Background(), claim("requester", "aa:bb:cc:dd:ee:ff", 11000))
	if res.Status != "proceed" {
		t.Fatalf("status = %q", res.Status)
	}
	if selected != "macbook-b" {
		t.Fatalf("selected peer = %q, want macbook-b", selected)
	}
}

func TestClaimLockBusyAndExpires(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	core := NewCore("token", func(_ context.Context, _ string, req protocol.Release) (protocol.ReleaseReply, error) {
		return protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "started"}, nil
	})
	core.SetClock(clock)
	registerNode(core, "requester", "aa:bb:cc:dd:ee:ff")
	registerNode(core, "peer", "aa:bb:cc:dd:ee:ff")

	if res := core.HandleClaim(context.Background(), claim("requester", "aa:bb:cc:dd:ee:ff", 11000)); res.Status != "proceed" {
		t.Fatalf("first status = %q", res.Status)
	}
	if res := core.HandleClaim(context.Background(), claim("requester", "aa:bb:cc:dd:ee:ff", 11000)); res.Status != protocol.ErrClaimBusy {
		t.Fatalf("second status = %q, want claim_busy", res.Status)
	}
	clock.now = clock.now.Add(12 * time.Second)
	if res := core.HandleClaim(context.Background(), claim("requester", "aa:bb:cc:dd:ee:ff", 11000)); res.Status != "proceed" {
		t.Fatalf("after expiry status = %q", res.Status)
	}
}

func TestPeerBusyClearsLock(t *testing.T) {
	core := NewCore("token", func(_ context.Context, _ string, req protocol.Release) (protocol.ReleaseReply, error) {
		return protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "busy"}, nil
	})
	registerNode(core, "requester", "aa:bb:cc:dd:ee:ff")
	registerNode(core, "peer", "aa:bb:cc:dd:ee:ff")

	if res := core.HandleClaim(context.Background(), claim("requester", "aa:bb:cc:dd:ee:ff", 11000)); res.Status != protocol.ErrPeerBusy {
		t.Fatalf("first status = %q", res.Status)
	}
	core.release = func(_ context.Context, _ string, req protocol.Release) (protocol.ReleaseReply, error) {
		return protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "started"}, nil
	}
	if res := core.HandleClaim(context.Background(), claim("requester", "aa:bb:cc:dd:ee:ff", 11000)); res.Status != "proceed" {
		t.Fatalf("second status = %q", res.Status)
	}
}

func TestReleaseUnconfirmedKeepsProtectedLock(t *testing.T) {
	core := NewCore("token", func(ctx context.Context, _ string, req protocol.Release) (protocol.ReleaseReply, error) {
		<-ctx.Done()
		return protocol.ReleaseReply{}, ctx.Err()
	})
	registerNode(core, "requester", "aa:bb:cc:dd:ee:ff")
	registerNode(core, "peer", "aa:bb:cc:dd:ee:ff")

	if res := core.HandleClaim(context.Background(), claim("requester", "aa:bb:cc:dd:ee:ff", 11000)); res.Status != protocol.ErrReleaseUnconfirmed {
		t.Fatalf("first status = %q", res.Status)
	}
	core.release = func(_ context.Context, _ string, req protocol.Release) (protocol.ReleaseReply, error) {
		return protocol.ReleaseReply{Protocol: protocol.Version, Type: protocol.TypeReleaseReply, RequestID: req.RequestID, Status: "started"}, nil
	}
	if res := core.HandleClaim(context.Background(), claim("requester", "aa:bb:cc:dd:ee:ff", 11000)); res.Status != protocol.ErrClaimBusy {
		t.Fatalf("second status = %q, want protected claim_busy", res.Status)
	}
}

func TestReleaseRequestErrorMarksPeerDead(t *testing.T) {
	core := NewCore("token", func(context.Context, string, protocol.Release) (protocol.ReleaseReply, error) {
		return protocol.ReleaseReply{}, errors.New("no responders")
	})
	registerNode(core, "requester", "aa:bb:cc:dd:ee:ff")
	registerNode(core, "peer", "aa:bb:cc:dd:ee:ff")

	if res := core.HandleClaim(context.Background(), claim("requester", "aa:bb:cc:dd:ee:ff", 11000)); res.Status != protocol.ErrReleaseUnconfirmed {
		t.Fatalf("status = %q", res.Status)
	}
	core.mu.Lock()
	live := core.nodes["peer"].live
	core.mu.Unlock()
	if live {
		t.Fatal("peer should be marked dead")
	}
}

func TestPeerUnavailable(t *testing.T) {
	core := NewCore("token", nil)
	registerNode(core, "requester", "aa:bb:cc:dd:ee:ff")
	if res := core.HandleClaim(context.Background(), claim("requester", "aa:bb:cc:dd:ee:ff", 11000)); res.Status != protocol.ErrPeerUnavailable {
		t.Fatalf("status = %q", res.Status)
	}
}

func registerNode(core *Core, node, address string) {
	core.Register(protocol.Register{Protocol: protocol.Version, Type: protocol.TypeRegister, Node: node, AuthToken: "token", Devices: map[string]string{"trackpad": address}})
}

func claim(requester, address string, remaining int64) protocol.Claim {
	id, _ := protocol.NewRequestID()
	return protocol.Claim{Protocol: protocol.Version, Type: protocol.TypeClaim, RequestID: id, Requester: requester, Device: address, RemainingMS: remaining}
}
