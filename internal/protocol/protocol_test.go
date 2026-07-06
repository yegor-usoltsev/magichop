package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAckSubject(t *testing.T) {
	t.Parallel()

	if got := AckSubject("abc"); got != "magichop.acks.abc" {
		t.Fatalf("unexpected ack subject: %s", got)
	}
}

func TestCommandJSON(t *testing.T) {
	t.Parallel()

	deadline := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	msg := CommandMessage{
		ID:            "cmd-1",
		Type:          CommandTypeReleaseDevice,
		FromNode:      "mac-a",
		DeviceAddress: "aa-bb-cc-dd-ee-ff",
		Deadline:      deadline,
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	var decoded CommandMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal command: %v", err)
	}
	if decoded != msg {
		t.Fatalf("decoded command mismatch: %#v", decoded)
	}
}

func TestClaimResponseJSON(t *testing.T) {
	t.Parallel()

	msg := ClaimResponse{ExpectedAcks: 2}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal claim response: %v", err)
	}
	var decoded ClaimResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal claim response: %v", err)
	}
	if decoded != msg {
		t.Fatalf("decoded claim response mismatch: %#v", decoded)
	}
}

func TestBluetoothAddressHelpers(t *testing.T) {
	t.Parallel()

	if !IsBluetoothAddress("AA:BB:CC:DD:EE:FF") {
		t.Fatal("expected valid colon address")
	}
	if !IsBluetoothAddress("aa-bb-cc-dd-ee-ff") {
		t.Fatal("expected valid hyphen address")
	}
	if IsBluetoothAddress("aa-bb") {
		t.Fatal("expected invalid short address")
	}
	if got := NormalizeBluetoothAddress("AA:BB:CC:DD:EE:FF"); got != "aa-bb-cc-dd-ee-ff" {
		t.Fatalf("unexpected normalized address: %s", got)
	}
	if got := MaskBluetoothAddress("AA:BB:CC:DD:EE:FF"); got != "aa-**-**-**-**-ff" {
		t.Fatalf("unexpected masked address: %s", got)
	}
	if got := MaskBluetoothAddress("nope"); got != "" {
		t.Fatalf("unexpected masked invalid address: %s", got)
	}
}

func TestClaimValidation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	valid := ClaimMessage{
		ID:            "cmd-1",
		FromNode:      "mac-a",
		DeviceAddress: "aa-bb-cc-dd-ee-ff",
		Deadline:      now.Add(time.Second),
	}
	if err := valid.Validate(now); err != nil {
		t.Fatalf("valid claim rejected: %v", err)
	}
	invalid := valid
	invalid.DeviceAddress = "not-a-mac"
	if err := invalid.Validate(now); err == nil {
		t.Fatal("expected invalid address error")
	}
	invalid = valid
	invalid.Deadline = now
	if err := invalid.Validate(now); err == nil {
		t.Fatal("expected expired deadline error")
	}
}

func TestNodeValidation(t *testing.T) {
	t.Parallel()

	if err := (NodeMessage{Node: "mac-a"}).Validate(); err != nil {
		t.Fatalf("valid node rejected: %v", err)
	}
	if err := (NodeMessage{}).Validate(); err == nil {
		t.Fatal("expected missing node error")
	}
}

func TestCommandValidation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	valid := CommandMessage{
		ID:            "cmd-1",
		Type:          CommandTypeReleaseDevice,
		FromNode:      "mac-a",
		DeviceAddress: "aa-bb-cc-dd-ee-ff",
		Deadline:      now.Add(time.Second),
	}
	if err := valid.Validate(now); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}
	invalid := valid
	invalid.Type = "other"
	if err := invalid.Validate(now); err == nil {
		t.Fatal("expected unsupported type error")
	}
	invalid = valid
	invalid.FromNode = ""
	if err := invalid.Validate(now); err == nil {
		t.Fatal("expected missing from_node error")
	}
}

func TestAckValidation(t *testing.T) {
	t.Parallel()

	valid := AckMessage{CommandID: "cmd-1", Node: "mac-b", OK: true}
	if err := valid.Validate("cmd-1"); err != nil {
		t.Fatalf("valid ack rejected: %v", err)
	}
	invalid := valid
	invalid.CommandID = "cmd-2"
	if err := invalid.Validate("cmd-1"); err == nil {
		t.Fatal("expected command mismatch error")
	}
	invalid = valid
	invalid.Node = ""
	if err := invalid.Validate("cmd-1"); err == nil {
		t.Fatal("expected missing node error")
	}
	invalid = AckMessage{CommandID: "cmd-1", Node: "mac-b"}
	if err := invalid.Validate("cmd-1"); err == nil {
		t.Fatal("expected missing failure error")
	}
}
