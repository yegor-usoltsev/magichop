package protocol

import (
	"bufio"
	"encoding/json"
	"io"
)

const (
	LocalClaim         = "claim"
	LocalAccepted      = "accepted"
	LocalFinalResult   = "final_result"
	LocalRelease       = "release"
	LocalReleaseResult = "release_result"
	LocalStatus        = "status"
	LocalStatusResult  = "status_result"
	LocalDevices       = "devices"
	LocalDevicesResult = "devices_result"
	LocalDoctor        = "doctor"
	LocalDoctorResult  = "doctor_result"
)

type LocalRequest struct {
	Type            string `json:"type"`
	ClientRequestID string `json:"client_request_id,omitempty"`
	Device          string `json:"device,omitempty"`
	TimeoutMS       int64  `json:"timeout_ms,omitempty"`
	Scan            bool   `json:"scan,omitempty"`
}

type Accepted struct {
	Type            string `json:"type"`
	ClientRequestID string `json:"client_request_id"`
	RequestID       string `json:"request_id"`
}

type FinalResult struct {
	Type        string `json:"type"`
	RequestID   string `json:"request_id"`
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	DurationMS  int64  `json:"duration_ms"`
	Device      string `json:"device,omitempty"`
	Address     string `json:"address,omitempty"`
	Connected   bool   `json:"connected,omitempty"`
	AcquireSeen bool   `json:"acquire_verified,omitempty"`
}

type ReleaseResult struct {
	Type       string `json:"type"`
	OK         bool   `json:"ok"`
	Device     string `json:"device"`
	Address    string `json:"address"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

type StatusResult struct {
	Type        string        `json:"type"`
	OK          bool          `json:"ok"`
	Daemon      string        `json:"daemon"`
	Coordinator string        `json:"coordinator"`
	Device      string        `json:"device,omitempty"`
	Connected   bool          `json:"connected,omitempty"`
	LastError   string        `json:"last_error,omitempty"`
	Recent      []FinalResult `json:"recent,omitempty"`
}

type DevicesResult struct {
	Type    string            `json:"type"`
	OK      bool              `json:"ok"`
	Devices map[string]string `json:"devices"`
	Error   string            `json:"error,omitempty"`
}

type DoctorCheck struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Err  string `json:"error,omitempty"`
}

type DoctorResult struct {
	Type   string        `json:"type"`
	OK     bool          `json:"ok"`
	Checks []DoctorCheck `json:"checks"`
}

func EncodeLine(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}

func DecodeLine(r *bufio.Reader, v any) error {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, v)
}
