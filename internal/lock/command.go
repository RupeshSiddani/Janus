package lock

import "time"

type CommandType string

const (
	CmdCreateLease CommandType = "create_lease"
	CmdRenewLease  CommandType = "renew_lease"
	CmdAcquireLock CommandType = "acquire_lock"
	CmdReleaseLock CommandType = "release_lock"
	CmdExpireLease CommandType = "expire_lease"
)

type Command struct {
	Type     CommandType   `json:"type"`
	OwnerID  string        `json:"owner_id,omitempty"`
	TTL      time.Duration `json:"ttl,omitempty"`
	LeaseID  uint64        `json:"lease_id,omitempty"`
	LockName string        `json:"lock_name,omitempty"`
}
