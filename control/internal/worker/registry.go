package worker

import (
	"github.com/mooncorn/dockyard/proto/pb"
)

type RegistryConfig struct {
	ConnectionStore *ConnectionStore
}

// Registry manages worker registration, provides worker lookup and status change events
type Registry struct {
	connectionStore *ConnectionStore
}

// NewRegistry creates a new Registry instance
func NewRegistry(config RegistryConfig) *Registry {
	return &Registry{
		connectionStore: config.ConnectionStore,
	}
}

// Register registers a new worker
func (m *Registry) Register(workerID string, stream pb.DockyardService_StreamCommunicationServer) error {
	workerConnection := Connection{
		ID:     workerID,
		Stream: stream,
		Status: StatusOnline,
	}

	m.connectionStore.Add(&workerConnection)

	return nil
}

func (m *Registry) Unregister(workerID string) {
	m.connectionStore.Remove(workerID)
}

func (r *Registry) IsOnline(workerID string) bool {
	workers := r.connectionStore.GetAll()

	if connection, exists := workers[workerID]; exists {
		return connection.Status == StatusOnline
	}
	return false
}
