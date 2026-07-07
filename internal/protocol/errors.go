package protocol

const (
	ErrBusy                   = "busy"
	ErrClaimBusy              = "claim_busy"
	ErrPeerBusy               = "peer_busy"
	ErrPeerUnavailable        = "peer_unavailable"
	ErrReleaseUnconfirmed     = "release_unconfirmed"
	ErrReleaseFailed          = "release_failed"
	ErrCoordinatorUnavailable = "coordinator_unavailable"
	ErrClaimTimeout           = "claim_timeout"
	ErrInvalidRequest         = "invalid_request"
	ErrInvalidResponse        = "invalid_response"
	ErrStaleRelease           = "stale_release"
	ErrBlueutilMissing        = "blueutil_missing"
	ErrPermissionDenied       = "permission_denied"
	ErrLogUnavailable         = "log_unavailable"
	ErrDaemonRestarted        = "daemon_restarted"
	ErrPairFailed             = "pair_failed"
	ErrConnectFailed          = "connect_failed"
	ErrVerifyFailed           = "verify_failed"
)
