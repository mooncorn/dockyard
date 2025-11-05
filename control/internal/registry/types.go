package registry

import (
	"context"
	"time"

	"github.com/mooncorn/dockyard/proto/pb"
)

// WorkerStatus represents the current status of a worker
type WorkerStatus string

const (
	WorkerStatusOnline  WorkerStatus = "online"
	WorkerStatusOffline WorkerStatus = "offline"
)

// Health monitoring constants
const (
	PingInterval   = 10 * time.Second
	PongTimeout    = 20 * time.Second
	MaxMissedPings = 2
)

// StatusChangedEvent represents a worker status change event
type StatusChangedEvent struct {
	WorkerID       string
	PreviousStatus string
	CurrentStatus  string
}

// WorkerConnection represents a connected worker
type WorkerConnection struct {
	ID               string
	Stream           pb.DockyardService_StreamCommunicationServer
	Status           WorkerStatus
	LastPingTime     time.Time
	PendingPingCount int
	CancelPing       context.CancelFunc
}
