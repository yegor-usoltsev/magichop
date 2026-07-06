package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/yegor-usoltsev/MagicHop/internal/bluetooth"
	"github.com/yegor-usoltsev/MagicHop/internal/config"
	"github.com/yegor-usoltsev/MagicHop/internal/protocol"
)

const (
	heartbeatInterval = time.Minute
	reconnectDelay    = time.Second
)

type Client struct {
	Config    config.ClientConfig
	Bluetooth bluetooth.Backend
}

type ClaimResult struct {
	CommandID string
	Acks      []protocol.AckMessage
	Connect   bluetooth.Result
}

func (c Client) Run(ctx context.Context) error {
	for {
		if err := c.runConnected(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("daemon connection failed; retrying", "err", err, "retry_after", reconnectDelay)
		}
		if err := sleep(ctx, reconnectDelay); err != nil {
			return nil
		}
	}
}

func (c Client) runConnected(ctx context.Context) error {
	nc, err := c.connect()
	if err != nil {
		return err
	}
	defer nc.Close()
	if _, err := nc.Subscribe(protocol.SubjectCommandsBroadcast, c.handleCommand(ctx, nc)); err != nil {
		return fmt.Errorf("subscribe commands: %w", err)
	}
	if err := nc.Flush(); err != nil {
		return fmt.Errorf("flush daemon subscription: %w", err)
	}
	if err := c.publishNode(nc, protocol.SubjectNodesAnnounce); err != nil {
		return err
	}
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	slog.Info("daemon started", "node", c.Config.NodeName, "coordinator_url", c.Config.CoordinatorURL)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.publishNode(nc, protocol.SubjectNodesHeartbeat); err != nil {
				slog.Warn("failed to publish heartbeat", "err", err)
			}
		}
	}
}

func (c Client) Claim(ctx context.Context, deviceRef string) (ClaimResult, error) {
	address, err := c.Config.ResolveDevice(deviceRef)
	if err != nil {
		return ClaimResult{}, fmt.Errorf("resolve device: %w", err)
	}
	nc, err := c.connect()
	if err != nil {
		return ClaimResult{}, err
	}
	defer nc.Close()

	return c.claimRound(ctx, nc, address, time.Now().Add(c.Config.ConnectTimeout.Duration))
}

func (c Client) claimRound(ctx context.Context, nc *nats.Conn, address string, overallDeadline time.Time) (ClaimResult, error) {
	commandID, err := randomID()
	if err != nil {
		return ClaimResult{}, err
	}
	ackCh := make(chan protocol.AckMessage, 16)
	sub, err := nc.Subscribe(protocol.AckSubject(commandID), func(msg *nats.Msg) {
		var ack protocol.AckMessage
		if err := json.Unmarshal(msg.Data, &ack); err != nil {
			slog.Warn("invalid ack", "err", err)
			return
		}
		if err := ack.Validate(commandID); err != nil {
			slog.Warn("rejected ack", "err", err)
			return
		}
		ackCh <- ack
	})
	if err != nil {
		return ClaimResult{}, fmt.Errorf("subscribe acks: %w", err)
	}
	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			slog.Warn("failed to unsubscribe acks", "err", err)
		}
	}()
	if err := nc.Flush(); err != nil {
		return ClaimResult{}, fmt.Errorf("flush ack subscription: %w", err)
	}

	roundDeadline := minTime(time.Now().Add(c.Config.ClaimTimeout.Duration), overallDeadline)
	if !roundDeadline.After(time.Now()) {
		return ClaimResult{}, fmt.Errorf("claim canceled: %w", context.DeadlineExceeded)
	}
	claim := protocol.ClaimMessage{
		ID:            commandID,
		FromNode:      c.Config.NodeName,
		DeviceAddress: address,
		Deadline:      roundDeadline.UTC(),
	}
	raw, err := json.Marshal(claim)
	if err != nil {
		return ClaimResult{}, fmt.Errorf("marshal claim: %w", err)
	}
	requestWait := time.Until(roundDeadline)
	if requestWait <= 0 {
		return ClaimResult{}, fmt.Errorf("claim canceled: %w", context.DeadlineExceeded)
	}
	msg, err := nc.Request(protocol.SubjectCommandsClaim, raw, requestTimeout(requestWait))
	if err != nil {
		return ClaimResult{}, fmt.Errorf("request claim: %w", err)
	}
	var response protocol.ClaimResponse
	if err := json.Unmarshal(msg.Data, &response); err != nil {
		return ClaimResult{}, fmt.Errorf("decode claim response: %w", err)
	}

	expected := response.ExpectedAcks
	acks := make([]protocol.AckMessage, 0, expected)
	seen := make(map[string]bool)
	if expected == 0 {
		result := c.Bluetooth.Connect(ctx, address, time.Until(overallDeadline))
		return ClaimResult{CommandID: commandID, Acks: acks, Connect: result}, nil
	}
	timer := time.NewTimer(time.Until(roundDeadline))
	defer timer.Stop()
	for len(acks) < expected {
		select {
		case <-ctx.Done():
			return ClaimResult{}, fmt.Errorf("claim canceled: %w", ctx.Err())
		case <-timer.C:
			result := c.Bluetooth.Connect(ctx, address, time.Until(overallDeadline))
			return ClaimResult{CommandID: commandID, Acks: sortedAcks(acks), Connect: result}, nil
		case ack := <-ackCh:
			if ack.Node == c.Config.NodeName || seen[ack.Node] {
				continue
			}
			seen[ack.Node] = true
			acks = append(acks, ack)
		}
	}
	result := c.Bluetooth.Connect(ctx, address, time.Until(overallDeadline))
	return ClaimResult{CommandID: commandID, Acks: sortedAcks(acks), Connect: result}, nil
}

func (c Client) Status(ctx context.Context) (bool, []protocol.NodeStatus, error) {
	connected, result := c.Bluetooth.IsConnected(ctx, c.defaultAddress(), c.Config.ConnectTimeout.Duration)
	if !result.OK {
		return false, nil, fmt.Errorf("check bluetooth: %s", result.Message())
	}
	nc, err := c.connect()
	if err != nil {
		return connected, nil, err
	}
	defer nc.Close()
	nodes, err := requestNodes(nc, 2*time.Second)
	if err != nil {
		return connected, nil, err
	}
	return connected, nodes, nil
}

func (c Client) connect() (*nats.Conn, error) {
	nc, err := nats.Connect(
		c.Config.CoordinatorURL,
		nats.Token(c.Config.AuthToken),
		nats.Name("magichop-"+c.Config.NodeName),
		nats.Timeout(2*time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(reconnectDelay),
	)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	return nc, nil
}

func (c Client) publishNode(nc *nats.Conn, subject string) error {
	host, _ := os.Hostname()
	msg := protocol.NodeMessage{
		Node:   c.Config.NodeName,
		Host:   host,
		SeenAt: time.Now().UTC(),
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal node: %w", err)
	}
	if err := nc.Publish(subject, raw); err != nil {
		return fmt.Errorf("publish node: %w", err)
	}
	if err := nc.Flush(); err != nil {
		return fmt.Errorf("flush node publish: %w", err)
	}
	return nil
}

func (c Client) handleCommand(ctx context.Context, nc *nats.Conn) nats.MsgHandler {
	return func(msg *nats.Msg) {
		var cmd protocol.CommandMessage
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			slog.Warn("invalid command", "err", err)
			return
		}
		if cmd.FromNode == c.Config.NodeName {
			return
		}
		ack := protocol.AckMessage{CommandID: cmd.ID, Node: c.Config.NodeName, OK: true}
		if err := cmd.Validate(time.Now().UTC()); err != nil {
			ack.OK = false
			ack.Error = err.Error()
		} else {
			result := c.Bluetooth.Release(ctx, cmd.DeviceAddress, c.Config.ReleaseTimeout.Duration)
			ack.OK = result.OK
			if !result.OK {
				ack.Error = result.Message()
			}
		}
		raw, err := json.Marshal(ack)
		if err != nil {
			slog.Error("failed to marshal ack", "err", err)
			return
		}
		if err := nc.Publish(protocol.AckSubject(cmd.ID), raw); err != nil {
			slog.Error("failed to publish ack", "err", err)
		}
	}
}

func requestNodes(nc *nats.Conn, timeout time.Duration) ([]protocol.NodeStatus, error) {
	msg, err := nc.Request(protocol.SubjectStatusRequest, nil, timeout)
	if err != nil {
		return nil, fmt.Errorf("request status: %w", err)
	}
	var status protocol.StatusResponse
	if err := json.Unmarshal(msg.Data, &status); err != nil {
		return nil, fmt.Errorf("decode status: %w", err)
	}
	sort.Slice(status.Nodes, func(i, j int) bool { return status.Nodes[i].Node < status.Nodes[j].Node })
	return status.Nodes, nil
}

func requestTimeout(claimTimeout time.Duration) time.Duration {
	if claimTimeout < time.Second {
		return claimTimeout
	}
	return time.Second
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("context done: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

func (c Client) defaultAddress() string {
	address, err := c.Config.ResolveDevice("")
	if err != nil {
		return ""
	}
	return address
}

func sortedAcks(acks []protocol.AckMessage) []protocol.AckMessage {
	sort.Slice(acks, func(i, j int) bool { return acks[i].Node < acks[j].Node })
	return acks
}
