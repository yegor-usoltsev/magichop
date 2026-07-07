package protocol

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-playground/validator/v10"
)

const Version = 1

const (
	TypeRegister       = "register"
	TypeRegisterResult = "register_result"
	TypeHeartbeat      = "heartbeat"
	TypeClaim          = "claim"
	TypeRelease        = "release"
	TypeReleaseReply   = "release_reply"
	TypeClaimResult    = "claim_result"
)

type Register struct {
	Protocol  int               `json:"protocol" validate:"required,eq=1"`
	Type      string            `json:"type" validate:"required,eq=register"`
	Node      string            `json:"node" validate:"required"`
	AuthToken string            `json:"auth_token" validate:"required"`
	Devices   map[string]string `json:"devices" validate:"required"`
}

type RegisterResult struct {
	Protocol int    `json:"protocol" validate:"required,eq=1"`
	Type     string `json:"type" validate:"required,eq=register_result"`
	Node     string `json:"node" validate:"required"`
	Status   string `json:"status" validate:"required"`
	Reason   string `json:"reason"`
}

type Heartbeat struct {
	Protocol  int    `json:"protocol" validate:"required,eq=1"`
	Type      string `json:"type" validate:"required,eq=heartbeat"`
	Node      string `json:"node" validate:"required"`
	AuthToken string `json:"auth_token" validate:"required"`
}

type Claim struct {
	Protocol    int    `json:"protocol" validate:"required,eq=1"`
	Type        string `json:"type" validate:"required,eq=claim"`
	RequestID   string `json:"request_id" validate:"required"`
	Requester   string `json:"requester" validate:"required"`
	Device      string `json:"device" validate:"required"`
	RemainingMS int64  `json:"remaining_ms" validate:"required,gt=0"`
}

type Release struct {
	Protocol        int    `json:"protocol" validate:"required,eq=1"`
	Type            string `json:"type" validate:"required,eq=release"`
	RequestID       string `json:"request_id" validate:"required"`
	Requester       string `json:"requester" validate:"required"`
	Device          string `json:"device" validate:"required"`
	ReleaseTTLMS    int64  `json:"release_ttl_ms" validate:"required,gt=0"`
	MaxStartDelayMS int64  `json:"max_start_delay_ms" validate:"required,gt=0"`
}

type ReleaseReply struct {
	Protocol  int    `json:"protocol" validate:"required,eq=1"`
	Type      string `json:"type" validate:"required,eq=release_reply"`
	RequestID string `json:"request_id" validate:"required"`
	Status    string `json:"status" validate:"required"`
	Reason    string `json:"reason"`
}

type ClaimResult struct {
	Protocol      int    `json:"protocol" validate:"required,eq=1"`
	Type          string `json:"type" validate:"required,eq=claim_result"`
	RequestID     string `json:"request_id" validate:"required"`
	Status        string `json:"status" validate:"required"`
	ReleaseWaitMS int64  `json:"release_wait_ms"`
	Reason        string `json:"reason"`
}

func MessageType(data []byte) (string, error) {
	var envelope struct {
		Protocol int    `json:"protocol"`
		Type     string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", err
	}
	if envelope.Protocol != Version {
		return "", fmt.Errorf("%w: protocol", errors.New(ErrInvalidRequest))
	}
	if envelope.Type == "" {
		return "", fmt.Errorf("%w: type", errors.New(ErrInvalidRequest))
	}
	return envelope.Type, nil
}

var shapeValidator = validator.New(validator.WithRequiredStructEnabled())

func ValidateShape(v any) error {
	if err := shapeValidator.Struct(v); err != nil {
		return fmt.Errorf("%w: %v", errors.New(ErrInvalidRequest), err)
	}
	return nil
}
