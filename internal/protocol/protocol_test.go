package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRequestIDShape(t *testing.T) {
	id, err := NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidRequestID(id) {
		t.Fatalf("invalid request id shape: %q", id)
	}
}

func TestMessageTypeIgnoresUnknownFields(t *testing.T) {
	typ, err := MessageType([]byte(`{"protocol":1,"type":"claim","unknown":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if typ != TypeClaim {
		t.Fatalf("type = %q, want %q", typ, TypeClaim)
	}
}

func TestMessageTypeRejectsMissingRequiredEnvelope(t *testing.T) {
	if _, err := MessageType([]byte(`{"protocol":1}`)); err == nil {
		t.Fatal("expected missing type to fail")
	}
}

func TestValidateShape(t *testing.T) {
	valid := Claim{Protocol: Version, Type: TypeClaim, RequestID: "8f72c6125a9d414d9f8f0fb2e5a19c22", Requester: "a", Device: "aa:bb:cc:dd:ee:ff", RemainingMS: 1000}
	if err := ValidateShape(valid); err != nil {
		t.Fatalf("valid claim failed: %v", err)
	}
	invalid := Claim{Protocol: Version, Type: TypeClaim}
	if err := ValidateShape(invalid); err == nil {
		t.Fatal("expected invalid claim shape to fail")
	}
}

func TestLocalIPCEncodeDecode(t *testing.T) {
	var buf bytes.Buffer
	req := LocalRequest{Type: LocalClaim, ClientRequestID: "abc", Device: "trackpad", TimeoutMS: 11000}
	if err := EncodeLine(&buf, req); err != nil {
		t.Fatal(err)
	}
	var got LocalRequest
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != LocalClaim || got.Device != "trackpad" || got.TimeoutMS != 11000 {
		t.Fatalf("unexpected request: %+v", got)
	}
}
