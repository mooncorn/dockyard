package worker

import (
	"context"
	"time"

	"github.com/mooncorn/dockyard/proto/pb"
)

// WorkerStatus represents the current status of a worker
type Status string

const (
	StatusOnline  Status = "online"
	StatusOffline Status = "offline"
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
type Connection struct {
	ID     string
	Stream pb.DockyardService_StreamCommunicationServer
	Status Status

	UsedCpuCores float64
	UsedMemoryMb int64
	Containers   []*pb.ContainerStats

	sendCh       chan *pb.ControlMessage // Channel for sending messages (managed by ConnectionStore)
	cancelSender context.CancelFunc      // Cancel function for sender goroutine
}
