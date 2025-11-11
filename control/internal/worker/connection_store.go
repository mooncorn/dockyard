package worker

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/mooncorn/dockyard/proto/pb"
)

var (
	// ErrChannelFull is returned when the send channel buffer is full
	ErrChannelFull = errors.New("send channel is full")
	// ErrWorkerNotFound is returned when a worker is not found in the store
	ErrWorkerNotFound = errors.New("worker not found")
)

const (
	// SendChannelBufferSize is the buffer size for each worker's send channel
	SendChannelBufferSize = 256
)

// ConnectionStore provides thread-safe storage for worker connections
type ConnectionStore struct {
	mu          sync.RWMutex
	connections map[string]*Connection
}

// NewConnectionStore creates a new connection store
func NewConnectionStore() *ConnectionStore {
	return &ConnectionStore{
		connections: make(map[string]*Connection),
	}
}

// Add adds a new worker connection to the store and starts its sender goroutine
func (s *ConnectionStore) Add(conn *Connection) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Initialize the send channel
	conn.sendCh = make(chan *pb.ControlMessage, SendChannelBufferSize)

	// Start the sender goroutine
	ctx, cancel := context.WithCancel(context.Background())
	conn.cancelSender = cancel
	go conn.startSender(ctx)

	s.connections[conn.ID] = conn
}

// Remove removes a worker connection from the store and stops its sender goroutine
func (s *ConnectionStore) Remove(workerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if conn, exists := s.connections[workerID]; exists {
		// Stop the sender goroutine
		if conn.cancelSender != nil {
			conn.cancelSender()
		}
		// Close the send channel
		if conn.sendCh != nil {
			close(conn.sendCh)
		}
		delete(s.connections, workerID)
	}
}

// Get retrieves a worker connection from the store
func (s *ConnectionStore) Get(workerID string) (*Connection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	conn, exists := s.connections[workerID]
	return conn, exists
}

// GetAll returns a copy of all worker connections
func (s *ConnectionStore) GetAll() map[string]*Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]*Connection, len(s.connections))
	for id, conn := range s.connections {
		result[id] = conn
	}
	return result
}

// Update applies updates to a worker connection in a thread-safe manner
// The updateFn is called with the worker connection under write lock
// Returns ErrWorkerNotFound if the worker is not in the store
func (s *ConnectionStore) Update(workerID string, updateFn func(*Connection)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	conn, exists := s.connections[workerID]
	if !exists {
		return ErrWorkerNotFound
	}

	updateFn(conn)
	return nil
}

// Send sends a message to a worker via its send channel
// Returns ErrChannelFull if the channel buffer is full
// Returns ErrWorkerNotFound if the worker is not in the store
func (s *ConnectionStore) Send(workerID string, msg *pb.ControlMessage) error {
	s.mu.RLock()
	conn, exists := s.connections[workerID]
	s.mu.RUnlock()

	if !exists {
		return ErrWorkerNotFound
	}

	// Non-blocking send
	select {
	case conn.sendCh <- msg:
		return nil
	default:
		return ErrChannelFull
	}
}

// startSender runs in a goroutine and sends messages from the channel to the stream
func (conn *Connection) startSender(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			// Cleanup: drain remaining messages
			for len(conn.sendCh) > 0 {
				<-conn.sendCh
			}
			return
		case msg, ok := <-conn.sendCh:
			if !ok {
				// Channel closed
				return
			}
			if err := conn.Stream.Send(msg); err != nil {
				log.Printf("Error sending message to worker %s: %v", conn.ID, err)
				// Continue processing other messages
			}
		}
	}
}
