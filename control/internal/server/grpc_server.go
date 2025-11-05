package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/mooncorn/dockyard/control/internal/auth"
	"github.com/mooncorn/dockyard/control/internal/interfaces"
	"github.com/mooncorn/dockyard/proto/pb"
)

type WorkerStatus string

const (
	WorkerStatusOnline  WorkerStatus = "online"
	WorkerStatusOffline WorkerStatus = "offline"
)

const (
	PingInterval   = 10 * time.Second
	PongTimeout    = 20 * time.Second
	MaxMissedPings = 2
)

type StatusChangedEvent struct {
	WorkerID       string
	PreviousStatus string
	CurrentStatus  string
}

type GRPCServer struct {
	pb.UnimplementedDockyardServiceServer
	authenticator     auth.Authenticator
	workerConnections map[string]WorkerConnection
	mu                sync.RWMutex

	observers   []interfaces.Observer[StatusChangedEvent]
	observersMu sync.RWMutex
}

type WorkerConnection struct {
	ID               string
	Stream           pb.DockyardService_StreamCommunicationServer
	Status           WorkerStatus
	LastPingTime     time.Time
	PendingPingCount int
	CancelPing       context.CancelFunc
}

type GRPCServerConfig struct {
	Authenticator auth.Authenticator
}

func NewGRPCServer(config GRPCServerConfig) *GRPCServer {
	return &GRPCServer{
		authenticator:     config.Authenticator,
		workerConnections: make(map[string]WorkerConnection),
		observers:         make([]interfaces.Observer[StatusChangedEvent], 0),
	}
}

func (g *GRPCServer) StreamCommunication(stream pb.DockyardService_StreamCommunicationServer) error {
	// Authenticate worker from stream context
	workerID, err := g.authenticator.Authenticate(stream.Context())
	if err != nil {
		return fmt.Errorf("authentication failed")
	}

	// Check if worker is already connected
	g.mu.RLock()
	if existingConnection, exists := g.workerConnections[workerID]; exists && existingConnection.Status == WorkerStatusOnline {
		g.mu.RUnlock()
		log.Printf("Worker %s rejected: already connected", workerID)
		return fmt.Errorf("worker %s is already connected", workerID)
	}
	g.mu.RUnlock()

	log.Printf("Worker %s connected via communication stream", workerID)

	workerConnection := WorkerConnection{
		ID:     workerID,
		Stream: stream,
		Status: WorkerStatusOnline,
	}

	g.mu.Lock()
	g.workerConnections[workerID] = workerConnection
	g.mu.Unlock()

	// Notify observers that worker came online
	event := StatusChangedEvent{
		WorkerID:       workerID,
		PreviousStatus: string(WorkerStatusOffline),
		CurrentStatus:  string(WorkerStatusOnline),
	}
	go g.NotifyObservers(event)

	// Start ping loop for this worker
	g.startWorkerPingLoop(workerID)

	// Cleanup on disconnect
	defer func() {
		// Stop ping loop first
		g.stopWorkerPingLoop(workerID)

		g.mu.Lock()
		delete(g.workerConnections, workerID)
		g.mu.Unlock()

		// Notify observers that worker went offline
		offlineEvent := StatusChangedEvent{
			WorkerID:       workerID,
			PreviousStatus: string(WorkerStatusOnline),
			CurrentStatus:  string(WorkerStatusOffline),
		}
		go g.NotifyObservers(offlineEvent)
		log.Printf("Worker %s went offline", workerID)
	}()

	// Create context that cancels when the stream is done
	streamCtx, streamCancel := context.WithCancel(stream.Context())
	defer streamCancel()

	// Handle incoming messages from worker
	for {
		// Check if context is cancelled
		if streamCtx.Err() != nil {
			if errors.Is(streamCtx.Err(), context.Canceled) {
				log.Printf("\nstream context cancelled for worker: %s", workerID)
			} else {
				log.Printf("\nstream context error for worker %s: %v", workerID, streamCtx.Err())
			}
			return streamCtx.Err()
		}

		workerMsg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				log.Printf("\nWorker %s closed communication stream", workerID)
				break
			}
			log.Printf("\nError receiving message from worker %s: %v", workerID, err)
			return err
		}

		// Handle different message types
		switch msg := workerMsg.Message.(type) {
		case *pb.WorkerMessage_Pong:
			g.handlePongReceived(workerID, msg.Pong)
		default:
			log.Printf("Unknown message type received from agent %s: %T", workerID, msg)
		}
	}

	return nil
}

// Subscribe adds an observer to receive status change notifications
func (g *GRPCServer) Subscribe(observer interfaces.Observer[StatusChangedEvent]) {
	g.observersMu.Lock()
	defer g.observersMu.Unlock()
	g.observers = append(g.observers, observer)
}

// Unsubscribe removes an observer from receiving status change notifications
func (g *GRPCServer) Unsubscribe(observer interfaces.Observer[StatusChangedEvent]) {
	g.observersMu.Lock()
	defer g.observersMu.Unlock()

	for i, obs := range g.observers {
		if obs == observer {
			g.observers = append(g.observers[:i], g.observers[i+1:]...)
			break
		}
	}
}

// NotifyObservers sends status change events to all registered observers
func (g *GRPCServer) NotifyObservers(event StatusChangedEvent) {
	g.observersMu.RLock()
	observers := make([]interfaces.Observer[StatusChangedEvent], len(g.observers))
	copy(observers, g.observers)
	g.observersMu.RUnlock()

	for _, observer := range observers {
		go observer.OnEvent(event)
	}
}

// setWorkerStatus safely updates a worker's status and notifies observers
func (g *GRPCServer) setWorkerStatus(workerID string, newStatus WorkerStatus) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if connection, exists := g.workerConnections[workerID]; exists {
		oldStatus := connection.Status
		if oldStatus != newStatus {
			connection.Status = newStatus
			g.workerConnections[workerID] = connection

			// Notify observers of status change
			event := StatusChangedEvent{
				WorkerID:       workerID,
				PreviousStatus: string(oldStatus),
				CurrentStatus:  string(newStatus),
			}

			go g.NotifyObservers(event)
			log.Printf("Worker %s status changed: %s -> %s", workerID, oldStatus, newStatus)
		}
	}
}

// startWorkerPingLoop starts a ping loop for a specific worker
func (g *GRPCServer) startWorkerPingLoop(workerID string) {
	pingCtx, cancelPing := context.WithCancel(context.Background())

	// Store cancel function in worker connection
	g.mu.Lock()
	if connection, exists := g.workerConnections[workerID]; exists {
		connection.CancelPing = cancelPing
		g.workerConnections[workerID] = connection
	} else {
		g.mu.Unlock()
		cancelPing()
		return
	}
	g.mu.Unlock()

	go func() {
		ticker := time.NewTicker(PingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-pingCtx.Done():
				log.Printf("Ping loop stopped for worker %s", workerID)
				return
			case <-ticker.C:
				g.sendPingToWorker(workerID)
			}
		}
	}()
}

// sendPingToWorker sends a ping to a specific worker and handles timeout
func (g *GRPCServer) sendPingToWorker(workerID string) {
	g.mu.Lock()
	connection, exists := g.workerConnections[workerID]
	if !exists {
		g.mu.Unlock()
		return
	}

	// Check if too many pings are pending
	if connection.PendingPingCount >= MaxMissedPings {
		g.mu.Unlock()
		log.Printf("Worker %s missed %d pings, removing from connections", workerID, MaxMissedPings)
		g.removeWorkerDueToTimeout(workerID)
		return
	}

	// Update pending ping count and last ping time
	connection.PendingPingCount++
	connection.LastPingTime = time.Now()
	g.workerConnections[workerID] = connection
	g.mu.Unlock()

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
		g.removeWorkerDueToTimeout(workerID)
	} else {
		log.Printf("Sent ping to worker %s (pending: %d)", workerID, connection.PendingPingCount)
	}
}

// handlePongReceived processes a pong message from a worker
func (g *GRPCServer) handlePongReceived(workerID string, pong *pb.Pong) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if connection, exists := g.workerConnections[workerID]; exists {
		// Reset pending ping count since worker responded
		connection.PendingPingCount = 0
		g.workerConnections[workerID] = connection

		log.Printf("Received pong from worker %s: ping_ts=%d, pong_ts=%d (pending reset to 0)",
			workerID, pong.PingTimestamp, pong.Timestamp)
	}
}

// removeWorkerDueToTimeout removes a worker that failed health checks
func (g *GRPCServer) removeWorkerDueToTimeout(workerID string) {
	g.mu.Lock()
	connection, exists := g.workerConnections[workerID]
	if !exists {
		g.mu.Unlock()
		return
	}

	// Cancel ping loop
	if connection.CancelPing != nil {
		connection.CancelPing()
	}

	// Remove from connections
	delete(g.workerConnections, workerID)
	g.mu.Unlock()

	// Notify observers
	event := StatusChangedEvent{
		WorkerID:       workerID,
		PreviousStatus: string(WorkerStatusOnline),
		CurrentStatus:  string(WorkerStatusOffline),
	}
	go g.NotifyObservers(event)

	log.Printf("Worker %s removed due to ping timeout", workerID)
}

// stopWorkerPingLoop stops the ping loop for a worker
func (g *GRPCServer) stopWorkerPingLoop(workerID string) {
	g.mu.RLock()
	connection, exists := g.workerConnections[workerID]
	g.mu.RUnlock()

	if exists && connection.CancelPing != nil {
		connection.CancelPing()
		log.Printf("Stopped ping loop for worker %s", workerID)
	}
}
