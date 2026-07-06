package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/yegor-usoltsev/MagicHop/internal/config"
	"github.com/yegor-usoltsev/MagicHop/internal/protocol"
)

const nodeTTL = 45 * time.Second

type Coordinator struct {
	cfg config.ServerConfig
	srv *server.Server

	mu    sync.Mutex
	nodes map[string]protocol.NodeStatus
}

func New(cfg config.ServerConfig) *Coordinator {
	return &Coordinator{cfg: cfg, nodes: make(map[string]protocol.NodeStatus)}
}

func (c *Coordinator) Run(ctx context.Context) error {
	addr := config.CoordinatorAddress(c.cfg.ServerHost, c.cfg.ServerPort)
	slog.Info("starting coordinator", "addr", addr)
	opts := &server.Options{
		Host:          c.cfg.ServerHost,
		Port:          int(c.cfg.ServerPort),
		Authorization: c.cfg.AuthToken,
		NoSigs:        true,
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		return fmt.Errorf("create nats server: %w", err)
	}
	c.srv = srv
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		return fmt.Errorf("nats server not ready on %s", addr)
	}
	defer func() {
		srv.Shutdown()
		slog.Info("coordinator stopped", "addr", addr)
	}()

	nc, err := nats.Connect(config.LocalhostNATSURL(c.cfg.ServerPort), nats.Token(c.cfg.AuthToken))
	if err != nil {
		return fmt.Errorf("connect coordinator client: %w", err)
	}
	defer nc.Close()
	if err := c.subscribe(nc); err != nil {
		return err
	}
	slog.Info("coordinator started", "addr", addr)
	<-ctx.Done()
	return nil
}

func (c *Coordinator) subscribe(nc *nats.Conn) error {
	if _, err := nc.Subscribe(protocol.SubjectNodesAnnounce, c.handleNode); err != nil {
		return fmt.Errorf("subscribe node announce: %w", err)
	}
	if _, err := nc.Subscribe(protocol.SubjectNodesHeartbeat, c.handleNode); err != nil {
		return fmt.Errorf("subscribe node heartbeat: %w", err)
	}
	if _, err := nc.Subscribe(protocol.SubjectCommandsClaim, c.handleClaim(nc)); err != nil {
		return fmt.Errorf("subscribe claim commands: %w", err)
	}
	if _, err := nc.Subscribe(protocol.SubjectStatusRequest, c.handleStatus(nc)); err != nil {
		return fmt.Errorf("subscribe status requests: %w", err)
	}
	if err := nc.Flush(); err != nil {
		return fmt.Errorf("flush subscriptions: %w", err)
	}
	return nil
}

func (c *Coordinator) handleNode(msg *nats.Msg) {
	var node protocol.NodeMessage
	if err := json.Unmarshal(msg.Data, &node); err != nil {
		slog.Warn("invalid node message", "subject", msg.Subject, "err", err)
		return
	}
	if err := node.Validate(); err != nil {
		slog.Warn("rejected node message", "subject", msg.Subject, "node", node.Node, "err", err)
		return
	}
	seenAt := time.Now().UTC()
	c.mu.Lock()
	_, known := c.nodes[node.Node]
	c.nodes[node.Node] = protocol.NodeStatus{Node: node.Node, Host: node.Host, SeenAt: seenAt}
	c.mu.Unlock()
	if known {
		slog.Info("node heartbeat", "node", node.Node, "host", node.Host, "seen_at", seenAt)
		return
	}
	slog.Info("node registered", "node", node.Node, "host", node.Host, "seen_at", seenAt)
}

func (c *Coordinator) handleClaim(nc *nats.Conn) nats.MsgHandler {
	return func(msg *nats.Msg) {
		var claim protocol.ClaimMessage
		if err := json.Unmarshal(msg.Data, &claim); err != nil {
			slog.Warn("invalid claim message", "err", err)
			return
		}
		if err := claim.Validate(time.Now().UTC()); err != nil {
			slog.Warn("rejected claim message", "id", claim.ID, "from_node", claim.FromNode, "err", err)
			return
		}
		expectedAcks := c.commandSubscribers(claim.FromNode)
		slog.Info(
			"claim received",
			"id", claim.ID,
			"from_node", claim.FromNode,
			"device", protocol.MaskBluetoothAddress(claim.DeviceAddress),
			"expected_acks", expectedAcks,
			"deadline", claim.Deadline,
		)
		cmd := protocol.CommandMessage{
			ID:            claim.ID,
			Type:          protocol.CommandTypeDisconnectDevice,
			FromNode:      claim.FromNode,
			DeviceAddress: claim.DeviceAddress,
			Deadline:      claim.Deadline,
		}
		raw, err := json.Marshal(cmd)
		if err != nil {
			slog.Error("failed to marshal broadcast", "err", err)
			return
		}
		if err := nc.Publish(protocol.SubjectCommandsBroadcast, raw); err != nil {
			slog.Error("failed to broadcast command", "err", err)
			return
		}
		slog.Info("claim broadcasted", "id", claim.ID, "from_node", claim.FromNode, "expected_acks", expectedAcks)
		if msg.Reply != "" {
			c.replyClaim(nc, msg.Reply, claim.ID, expectedAcks)
		}
	}
}

func (c *Coordinator) replyClaim(nc *nats.Conn, reply, claimID string, expectedAcks int) {
	response := protocol.ClaimResponse{ExpectedAcks: expectedAcks}
	raw, err := json.Marshal(response)
	if err != nil {
		slog.Error("failed to marshal claim response", "id", claimID, "err", err)
		return
	}
	if err := nc.Publish(reply, raw); err != nil {
		slog.Error("failed to publish claim response", "id", claimID, "err", err)
		return
	}
	slog.Info("claim response sent", "id", claimID, "expected_acks", expectedAcks)
}

func (c *Coordinator) commandSubscribers(fromNode string) int {
	connz, err := c.srv.Connz(&server.ConnzOptions{Subscriptions: true})
	if err != nil {
		slog.Warn("failed to inspect command subscribers", "err", err)
		return 0
	}
	count := 0
	for _, conn := range connz.Conns {
		if conn.Name == "magichop-"+fromNode {
			continue
		}
		if slices.Contains(conn.Subs, protocol.SubjectCommandsBroadcast) {
			count++
		}
	}
	return count
}

func (c *Coordinator) handleStatus(nc *nats.Conn) nats.MsgHandler {
	return func(msg *nats.Msg) {
		status := protocol.StatusResponse{Nodes: c.recentNodes(time.Now().UTC())}
		raw, err := json.Marshal(status)
		if err != nil {
			slog.Error("failed to marshal status", "err", err)
			return
		}
		if err := nc.Publish(msg.Reply, raw); err != nil {
			slog.Error("failed to publish status", "err", err)
			return
		}
		slog.Info("status response sent", "nodes", len(status.Nodes))
	}
}

func (c *Coordinator) recentNodes(now time.Time) []protocol.NodeStatus {
	c.mu.Lock()
	defer c.mu.Unlock()

	nodes := make([]protocol.NodeStatus, 0, len(c.nodes))
	for name, node := range c.nodes {
		if now.Sub(node.SeenAt) > nodeTTL {
			delete(c.nodes, name)
			slog.Info("node expired", "node", name, "last_seen", node.SeenAt)
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes
}
