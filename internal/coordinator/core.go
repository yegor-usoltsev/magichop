package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/yegor-usoltsev/magichop/internal/protocol"
)

const (
	SubjectRegister = "mh.v1.register"
	ReasonNoPeer    = "no_peer"
)

func SubjectHeartbeat(node string) string {
	return "mh.v1.heartbeat." + node
}

func SubjectClaim(address string) string {
	return "mh.v1.claim." + DeviceHash(address)
}

func SubjectRelease(node string) string {
	return "mh.v1.release." + node
}

func DeviceHash(address string) string {
	sum := sha256.Sum256([]byte(address))
	return hex.EncodeToString(sum[:])[:16]
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

type PeerReleaseFunc func(ctx context.Context, peer string, req protocol.Release) (protocol.ReleaseReply, error)

type Core struct {
	mu        sync.Mutex
	authToken string
	clock     Clock
	nodes     map[string]nodeRecord
	locks     map[string]claimLock
	release   PeerReleaseFunc
}

type nodeRecord struct {
	name          string
	devices       map[string]string
	lastHeartbeat time.Time
	live          bool
	generation    uint64
}

type claimLock struct {
	requestID string
	deadline  time.Time
}

func NewCore(authToken string, release PeerReleaseFunc) *Core {
	return &Core{
		authToken: authToken,
		clock:     SystemClock{},
		nodes:     map[string]nodeRecord{},
		locks:     map[string]claimLock{},
		release:   release,
	}
}

func (c *Core) SetClock(clock Clock) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clock = clock
}

func (c *Core) Register(req protocol.Register) protocol.RegisterResult {
	if req.Protocol != protocol.Version || req.Type != protocol.TypeRegister || req.AuthToken != c.authToken || req.Node == "" {
		return protocol.RegisterResult{Protocol: protocol.Version, Type: protocol.TypeRegisterResult, Node: req.Node, Status: protocol.ErrInvalidRequest, Reason: protocol.ErrInvalidRequest}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.nodes[req.Node]
	devices := make(map[string]string, len(req.Devices))
	for alias, address := range req.Devices {
		devices[alias] = address
	}
	c.nodes[req.Node] = nodeRecord{name: req.Node, devices: devices, lastHeartbeat: c.clock.Now(), live: true, generation: old.generation + 1}
	return protocol.RegisterResult{Protocol: protocol.Version, Type: protocol.TypeRegisterResult, Node: req.Node, Status: "ok"}
}

func (c *Core) Heartbeat(req protocol.Heartbeat) error {
	if req.Protocol != protocol.Version || req.Type != protocol.TypeHeartbeat || req.AuthToken != c.authToken || req.Node == "" {
		return fmt.Errorf(protocol.ErrInvalidRequest)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	node, ok := c.nodes[req.Node]
	if !ok || !node.live {
		return fmt.Errorf(protocol.ErrInvalidRequest)
	}
	node.lastHeartbeat = c.clock.Now()
	c.nodes[req.Node] = node
	return nil
}

func (c *Core) MarkDead(node string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	record := c.nodes[node]
	record.live = false
	c.nodes[node] = record
}

func (c *Core) ReapDeadNodes() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock.Now()
	for name, node := range c.nodes {
		if node.live && now.Sub(node.lastHeartbeat) > DeadPeerAfter {
			node.live = false
			c.nodes[name] = node
		}
	}
}

func (c *Core) HandleClaim(ctx context.Context, req protocol.Claim) protocol.ClaimResult {
	if err := validateClaim(req); err != nil {
		return claimResult(req.RequestID, protocol.ErrInvalidRequest, err.Error())
	}
	now := c.now()
	deadline := now.Add(time.Duration(req.RemainingMS) * time.Millisecond)
	if time.Until(deadline) < NewClaimMinimum && c.clock == (SystemClock{}) {
		return claimResult(req.RequestID, protocol.ErrClaimTimeout, "remaining budget below minimum")
	}
	if deadline.Sub(now) < NewClaimMinimum {
		return claimResult(req.RequestID, protocol.ErrClaimTimeout, "remaining budget below minimum")
	}

	peer, locked := c.prepareClaim(req, deadline)
	if locked == protocol.ErrClaimBusy {
		return claimResult(req.RequestID, protocol.ErrClaimBusy, protocol.ErrClaimBusy)
	}
	if locked == protocol.ErrPeerUnavailable || peer == "" {
		return protocol.ClaimResult{Protocol: protocol.Version, Type: protocol.TypeClaimResult, RequestID: req.RequestID, Status: "proceed", ReleaseWaitMS: 0, Reason: ReasonNoPeer}
	}

	releaseReq := protocol.Release{
		Protocol:        protocol.Version,
		Type:            protocol.TypeRelease,
		RequestID:       req.RequestID,
		Requester:       req.Requester,
		Device:          req.Device,
		ReleaseTTLMS:    ReleaseWindow.Milliseconds(),
		MaxStartDelayMS: ReleaseStartWait.Milliseconds(),
	}
	reply, err := c.releaseWithTimeout(ctx, peer, releaseReq)
	if err != nil || reply.Status != "started" {
		if err != nil {
			c.MarkDead(peer)
		}
		status := protocol.ErrReleaseUnconfirmed
		if err == nil {
			switch reply.Status {
			case "busy":
				status = protocol.ErrPeerBusy
			case "fail":
				status = protocol.ErrReleaseFailed
			}
		}
		if status == protocol.ErrPeerBusy || status == protocol.ErrReleaseFailed {
			c.clearLock(req.Device, req.RequestID)
		}
		return claimResult(req.RequestID, status, status)
	}
	return protocol.ClaimResult{Protocol: protocol.Version, Type: protocol.TypeClaimResult, RequestID: req.RequestID, Status: "proceed", ReleaseWaitMS: ReleaseWindow.Milliseconds()}
}

func (c *Core) prepareClaim(req protocol.Claim, deadline time.Time) (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropExpiredLocksLocked()
	if _, ok := c.locks[req.Device]; ok {
		return "", protocol.ErrClaimBusy
	}
	peer, ok := c.choosePeerLocked(req.Requester, req.Device)
	if !ok {
		return "", protocol.ErrPeerUnavailable
	}
	c.locks[req.Device] = claimLock{requestID: req.RequestID, deadline: deadline}
	return peer, ""
}

func (c *Core) releaseWithTimeout(ctx context.Context, peer string, req protocol.Release) (protocol.ReleaseReply, error) {
	if c.release == nil {
		return protocol.ReleaseReply{}, fmt.Errorf(protocol.ErrReleaseUnconfirmed)
	}
	rctx, cancel := context.WithTimeout(ctx, ReleaseStartWait)
	defer cancel()
	type result struct {
		reply protocol.ReleaseReply
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		reply, err := c.release(rctx, peer, req)
		ch <- result{reply: reply, err: err}
	}()
	select {
	case out := <-ch:
		return out.reply, out.err
	case <-rctx.Done():
		return protocol.ReleaseReply{}, rctx.Err()
	}
}

func (c *Core) choosePeerLocked(requester, device string) (string, bool) {
	var peers []nodeRecord
	now := c.clock.Now()
	for _, node := range c.nodes {
		if !node.live || node.name == requester || now.Sub(node.lastHeartbeat) > DeadPeerAfter {
			continue
		}
		for _, address := range node.devices {
			if address == device {
				peers = append(peers, node)
				break
			}
		}
	}
	if len(peers) == 0 {
		return "", false
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].lastHeartbeat.Equal(peers[j].lastHeartbeat) {
			return peers[i].name < peers[j].name
		}
		return peers[i].lastHeartbeat.After(peers[j].lastHeartbeat)
	})
	return peers[0].name, true
}

func (c *Core) clearLock(device, requestID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock, ok := c.locks[device]
	if ok && lock.requestID == requestID {
		delete(c.locks, device)
	}
}

func (c *Core) dropExpiredLocksLocked() {
	now := c.clock.Now()
	for device, lock := range c.locks {
		if !now.Before(lock.deadline) {
			delete(c.locks, device)
		}
	}
}

func (c *Core) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clock.Now()
}

func validateClaim(req protocol.Claim) error {
	if req.Protocol != protocol.Version || req.Type != protocol.TypeClaim || req.Requester == "" || req.Device == "" || req.RemainingMS <= 0 || !protocol.ValidRequestID(req.RequestID) {
		return fmt.Errorf(protocol.ErrInvalidRequest)
	}
	return nil
}

func claimResult(requestID, status, reason string) protocol.ClaimResult {
	return protocol.ClaimResult{Protocol: protocol.Version, Type: protocol.TypeClaimResult, RequestID: requestID, Status: status, Reason: reason}
}
