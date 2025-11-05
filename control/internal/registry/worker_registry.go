package registry

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/mooncorn/dockyard/control/internal/interfaces"
	"github.com/mooncorn/dockyard/proto/pb"
)

// WorkerRegistry manages worker connections, status, and health monitoring
type WorkerRegistry interface {
	interfaces.Subject[StatusChangedEvent]

	// RegisterWorker registers a new worker connection
	RegisterWorker(workerID string, stream pb.DockyardService_StreamCommunicationServer) error

	// UnregisterWorker removes a worker connection
	UnregisterWorker(workerID string)

	// GetWorker returns a worker connection by ID
	GetWorker(workerID string) (WorkerConnection, bool)

	// ListWorkers returns all current worker connections
	ListWorkers() []WorkerConnection

	// HandlePongReceived processes a pong message from a worker
	HandlePongReceived(workerID string, pong *pb.Pong)

	// IsWorkerOnline checks if a worker is currently online
	IsWorkerOnline(workerID string) bool

	// Shutdown stops all ping loops and closes all worker connections
	Shutdown()
}

// workerRegistryImpl is the concrete implementation of WorkerRegistry
type workerRegistryImpl struct {
	workerConnections map[string]WorkerConnection
	mu                sync.RWMutex

	observers   []interfaces.Observer[StatusChangedEvent]
	observersMu sync.RWMutex
}

// NewWorkerRegistry creates a new WorkerRegistry instance
func NewWorkerRegistry() WorkerRegistry {
	return &workerRegistryImpl{
		workerConnections: make(map[string]WorkerConnection),
		observers:         make([]interfaces.Observer[StatusChangedEvent], 0),
	}
}

// RegisterWorker registers a new worker and starts health monitoring
func (r *workerRegistryImpl) RegisterWorker(workerID string, stream pb.DockyardService_StreamCommunicationServer) error {
	r.mu.Lock()

	// Check if worker is already connected
	if existingConnection, exists := r.workerConnections[workerID]; exists && existingConnection.Status == WorkerStatusOnline {
		r.mu.Unlock()
		log.Printf("Worker %s rejected: already connected", workerID)
		return nil // Return nil to allow graceful handling
	}

	workerConnection := WorkerConnection{
		ID:     workerID,
		Stream: stream,
		Status: WorkerStatusOnline,
	}

	r.workerConnections[workerID] = workerConnection
	r.mu.Unlock()

	log.Printf("Worker %s registered in registry", workerID)

	// Notify observers that worker came online
	event := StatusChangedEvent{
		WorkerID:       workerID,
		PreviousStatus: string(WorkerStatusOffline),
		CurrentStatus:  string(WorkerStatusOnline),
	}
	go r.NotifyObservers(event)

	// Start ping loop for this worker (after unlocking)
	r.startWorkerPingLoop(workerID)

	return nil
}

// UnregisterWorker removes a worker and stops health monitoring
func (r *workerRegistryImpl) UnregisterWorker(workerID string) {
	// Stop ping loop first
	r.stopWorkerPingLoop(workerID)

	r.mu.Lock()
	delete(r.workerConnections, workerID)
	r.mu.Unlock()

	// Notify observers that worker went offline
	offlineEvent := StatusChangedEvent{
		WorkerID:       workerID,
		PreviousStatus: string(WorkerStatusOnline),
		CurrentStatus:  string(WorkerStatusOffline),
	}
	go r.NotifyObservers(offlineEvent)
	log.Printf("Worker %s unregistered from registry", workerID)
}

// GetWorker returns a worker connection by ID
func (r *workerRegistryImpl) GetWorker(workerID string) (WorkerConnection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	connection, exists := r.workerConnections[workerID]
	return connection, exists
}

// ListWorkers returns all current worker connections
func (r *workerRegistryImpl) ListWorkers() []WorkerConnection {
	r.mu.RLock()
	defer r.mu.RUnlock()

	workers := make([]WorkerConnection, 0, len(r.workerConnections))
	for _, connection := range r.workerConnections {
		workers = append(workers, connection)
	}
	return workers
}

// IsWorkerOnline checks if a worker is currently online
func (r *workerRegistryImpl) IsWorkerOnline(workerID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if connection, exists := r.workerConnections[workerID]; exists {
		return connection.Status == WorkerStatusOnline
	}
	return false
}

// HandlePongReceived processes a pong message from a worker
func (r *workerRegistryImpl) HandlePongReceived(workerID string, pong *pb.Pong) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if connection, exists := r.workerConnections[workerID]; exists {
		// Reset pending ping count since worker responded
		connection.PendingPingCount = 0
		r.workerConnections[workerID] = connection

		log.Printf("Received pong from worker %s: ping_ts=%d, pong_ts=%d (pending reset to 0)",
			workerID, pong.PingTimestamp, pong.Timestamp)
	}
}

// Subscribe adds an observer to receive status change notifications
func (r *workerRegistryImpl) Subscribe(observer interfaces.Observer[StatusChangedEvent]) {
	r.observersMu.Lock()
	defer r.observersMu.Unlock()
	r.observers = append(r.observers, observer)
}

// Unsubscribe removes an observer from receiving status change notifications
func (r *workerRegistryImpl) Unsubscribe(observer interfaces.Observer[StatusChangedEvent]) {
	r.observersMu.Lock()
	defer r.observersMu.Unlock()

	for i, obs := range r.observers {
		if obs == observer {
			r.observers = append(r.observers[:i], r.observers[i+1:]...)
			break
		}
	}
}

// NotifyObservers sends status change events to all registered observers
func (r *workerRegistryImpl) NotifyObservers(event StatusChangedEvent) {
	r.observersMu.RLock()
	observers := make([]interfaces.Observer[StatusChangedEvent], len(r.observers))
	copy(observers, r.observers)
	r.observersMu.RUnlock()

	for _, observer := range observers {
		go observer.OnEvent(event)
	}
}

// setWorkerStatus safely updates a worker's status and notifies observers
func (r *workerRegistryImpl) setWorkerStatus(workerID string, newStatus WorkerStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if connection, exists := r.workerConnections[workerID]; exists {
		oldStatus := connection.Status
		if oldStatus != newStatus {
			connection.Status = newStatus
			r.workerConnections[workerID] = connection

			// Notify observers of status change
			event := StatusChangedEvent{
				WorkerID:       workerID,
				PreviousStatus: string(oldStatus),
				CurrentStatus:  string(newStatus),
			}

			go r.NotifyObservers(event)
			log.Printf("Worker %s status changed: %s -> %s", workerID, oldStatus, newStatus)
		}
	}
}

// startWorkerPingLoop starts a ping loop for a specific worker
func (r *workerRegistryImpl) startWorkerPingLoop(workerID string) {
	pingCtx, cancelPing := context.WithCancel(context.Background())

	// Store cancel function in worker connection
	r.mu.Lock()
	if connection, exists := r.workerConnections[workerID]; exists {
		connection.CancelPing = cancelPing
		r.workerConnections[workerID] = connection
	} else {
		r.mu.Unlock()
		cancelPing()
		return
	}
	r.mu.Unlock()

	go func() {
		ticker := time.NewTicker(PingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-pingCtx.Done():
				log.Printf("Ping loop stopped for worker %s", workerID)
				return
			case <-ticker.C:
				r.sendPingToWorker(workerID)
			}
		}
	}()
}

// sendPingToWorker sends a ping to a specific worker and handles timeout
func (r *workerRegistryImpl) sendPingToWorker(workerID string) {
	r.mu.Lock()
	connection, exists := r.workerConnections[workerID]
	if !exists {
		r.mu.Unlock()
		return
	}

	// Check if too many pings are pending
	if connection.PendingPingCount >= MaxMissedPings {
		r.mu.Unlock()
		log.Printf("Worker %s missed %d pings, removing from connections", workerID, MaxMissedPings)
		r.removeWorkerDueToTimeout(workerID)
		return
	}

	// Update pending ping count and last ping time
	connection.PendingPingCount++
	connection.LastPingTime = time.Now()
	r.workerConnections[workerID] = connection
	r.mu.Unlock()

	// Send ping message
	ping := &pb.ControlMessage{
		Message: &pb.ControlMessage_Ping{
			Ping: &pb.Ping{
				Timestamp: time.Now().UnixNano(),
			},
		},
	}

	err := connection.Stream.Send(ping)
	if err != nil {
		log.Printf("Failed to send ping to worker %s: %v", workerID, err)
		r.removeWorkerDueToTimeout(workerID)
	} else {
		log.Printf("Sent ping to worker %s (pending: %d)", workerID, connection.PendingPingCount)
	}
}

// stopWorkerPingLoop stops the ping loop for a worker
func (r *workerRegistryImpl) stopWorkerPingLoop(workerID string) {
	r.mu.RLock()
	connection, exists := r.workerConnections[workerID]
	r.mu.RUnlock()

	if exists && connection.CancelPing != nil {
		connection.CancelPing()
		log.Printf("Stopped ping loop for worker %s", workerID)
	}
}

// removeWorkerDueToTimeout removes a worker that failed health checks
func (r *workerRegistryImpl) removeWorkerDueToTimeout(workerID string) {
	r.mu.Lock()
	connection, exists := r.workerConnections[workerID]
	if !exists {
		r.mu.Unlock()
		return
	}

	// Cancel ping loop
	if connection.CancelPing != nil {
		connection.CancelPing()
	}

	// Remove from connections
	delete(r.workerConnections, workerID)
	r.mu.Unlock()

	// Notify observers
	event := StatusChangedEvent{
		WorkerID:       workerID,
		PreviousStatus: string(WorkerStatusOnline),
		CurrentStatus:  string(WorkerStatusOffline),
	}
	go r.NotifyObservers(event)

	log.Printf("Worker %s removed due to ping timeout", workerID)
}

// Shutdown stops all ping loops and cleans up all worker connections
func (r *workerRegistryImpl) Shutdown() {
	r.mu.Lock()
	workerIDs := make([]string, 0, len(r.workerConnections))
	for workerID := range r.workerConnections {
		workerIDs = append(workerIDs, workerID)
	}
	r.mu.Unlock()

	log.Printf("Shutting down worker registry, stopping %d worker connections", len(workerIDs))

	// Stop all ping loops and cancel contexts
	for _, workerID := range workerIDs {
		r.stopWorkerPingLoop(workerID)
	}

	// Clear all connections
	r.mu.Lock()
	r.workerConnections = make(map[string]WorkerConnection)
	r.mu.Unlock()

	log.Printf("Worker registry shutdown complete")
}
