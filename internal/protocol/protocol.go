package protocol

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	SubjectNodesAnnounce     = "magichop.nodes.announce"
	SubjectNodesHeartbeat    = "magichop.nodes.heartbeat"
	SubjectCommandsClaim     = "magichop.commands.claim"
	SubjectCommandsBroadcast = "magichop.commands.broadcast"
	SubjectStatusRequest     = "magichop.status.request"

	CommandTypeDisconnectDevice = "disconnect_device"
)

var bluetoothAddressPattern = regexp.MustCompile(`(?i)^[0-9a-f]{2}([-:][0-9a-f]{2}){5}$`)

func AckSubject(commandID string) string {
	return "magichop.acks." + commandID
}

func IsBluetoothAddress(value string) bool {
	return bluetoothAddressPattern.MatchString(strings.TrimSpace(value))
}

func NormalizeBluetoothAddress(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), ":", "-"))
}

func MaskBluetoothAddress(value string) string {
	normalized := NormalizeBluetoothAddress(value)
	if !IsBluetoothAddress(normalized) {
		return ""
	}
	return normalized[:2] + "-**-**-**-**-" + normalized[len(normalized)-2:]
}

type NodeMessage struct {
	Node     string    `json:"node"`
	Host     string    `json:"host"`
	SeenAt   time.Time `json:"seen_at"`
	ClientID string    `json:"client_id,omitempty"`
}

func (m NodeMessage) Validate() error {
	if m.Node == "" {
		return errors.New("node is required")
	}
	return nil
}

type ClaimMessage struct {
	ID            string    `json:"id"`
	FromNode      string    `json:"from_node"`
	DeviceAddress string    `json:"device_address"`
	Deadline      time.Time `json:"deadline"`
}

func (m ClaimMessage) Validate(now time.Time) error {
	if m.ID == "" {
		return errors.New("claim id is required")
	}
	if m.FromNode == "" {
		return errors.New("claim from_node is required")
	}
	if !IsBluetoothAddress(m.DeviceAddress) {
		return fmt.Errorf("claim device_address is invalid: %s", m.DeviceAddress)
	}
	if m.Deadline.IsZero() {
		return errors.New("claim deadline is required")
	}
	if !m.Deadline.After(now) {
		return errors.New("claim deadline has expired")
	}
	return nil
}

type ClaimResponse struct {
	ExpectedAcks int `json:"expected_acks"`
}

type CommandMessage struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"`
	FromNode      string    `json:"from_node"`
	DeviceAddress string    `json:"device_address"`
	Deadline      time.Time `json:"deadline"`
}

func (m CommandMessage) Validate(now time.Time) error {
	if m.ID == "" {
		return errors.New("command id is required")
	}
	if m.Type != CommandTypeDisconnectDevice {
		return fmt.Errorf("unsupported command type: %s", m.Type)
	}
	if m.FromNode == "" {
		return errors.New("command from_node is required")
	}
	if !IsBluetoothAddress(m.DeviceAddress) {
		return fmt.Errorf("command device_address is invalid: %s", m.DeviceAddress)
	}
	if m.Deadline.IsZero() {
		return errors.New("command deadline is required")
	}
	if !m.Deadline.After(now) {
		return errors.New("command deadline has expired")
	}
	return nil
}

type AckMessage struct {
	CommandID string `json:"command_id"`
	Node      string `json:"node"`
	OK        bool   `json:"ok"`
	Error     string `json:"error"`
}

func (m AckMessage) Validate(commandID string) error {
	if m.CommandID == "" {
		return errors.New("ack command_id is required")
	}
	if commandID != "" && m.CommandID != commandID {
		return fmt.Errorf("ack command_id mismatch: %s", m.CommandID)
	}
	if m.Node == "" {
		return errors.New("ack node is required")
	}
	if !m.OK && m.Error == "" {
		return errors.New("ack error is required when ok is false")
	}
	return nil
}

type StatusResponse struct {
	Nodes []NodeStatus `json:"nodes"`
}

type NodeStatus struct {
	Node   string    `json:"node"`
	Host   string    `json:"host"`
	SeenAt time.Time `json:"seen_at"`
}
