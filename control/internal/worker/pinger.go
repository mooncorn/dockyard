package worker

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/mooncorn/dockyard/proto/pb"
)

type PingerConfig struct {
	ConnectionStore *ConnectionStore
	Interval        time.Duration
	OnTimeout       func(workerID string) // Callback when worker exceeds max missed pings
}

// workerPingState tracks ping-related state for a single worker
type workerPingState struct {
	LastPingTime     time.Time
	PendingPingCount int
}

type Pinger struct {
	connectionStore *ConnectionStore
	interval        time.Duration
	onTimeout       func(workerID string)

	mu        sync.RWMutex
	pingState map[string]*workerPingState

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewPinger creates a new Pinger instance
func NewPinger(config PingerConfig) *Pinger {
	interval := config.Interval
	if interval == 0 {
		interval = PingInterval
	}

	return &Pinger{
		connectionStore: config.ConnectionStore,
		interval:        interval,
		onTimeout:       config.OnTimeout,
		pingState:       make(map[string]*workerPingState),
	}
}

// Start begins the periodic pinging of all connections
func (p *Pinger) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	p.wg.Add(1)
	go p.pingLoop(ctx)

	log.Printf("Pinger started with interval: %v", p.interval)
}

// Stop gracefully stops the pinger
func (p *Pinger) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	log.Printf("Pinger stopped")
}

// pingLoop runs the periodic ping ticker
func (p *Pinger) pingLoop(ctx context.Context) {
	defer p.wg.Done()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pingAll()
		}
	}
}

// pingAll pings all connections in the connection store
func (p *Pinger) pingAll() {
	connections := p.connectionStore.GetAll()

	// Track which workers are currently connected
	currentWorkerIDs := make(map[string]bool)
	for workerID := range connections {
		currentWorkerIDs[workerID] = true
	}

	// Clean up ping state for removed workers
	p.mu.Lock()
	for workerID := range p.pingState {
		if !currentWorkerIDs[workerID] {
			delete(p.pingState, workerID)
			log.Printf("Cleaned up ping state for removed worker %s", workerID)
		}
	}
	p.mu.Unlock()

	// Ping all current connections
	for workerID := range connections {
		p.pingWorker(workerID)
	}
}

// pingWorker sends a ping to a specific worker and checks for timeout
func (p *Pinger) pingWorker(workerID string) {
	// Get or initialize ping state
	p.mu.Lock()
	defer p.mu.Unlock()
	state, exists := p.pingState[workerID]
	if !exists {
		state = &workerPingState{}
		p.pingState[workerID] = state
	}

	// Check if worker has timed out (before sending another ping)
	if state.PendingPingCount >= MaxMissedPings {
		log.Printf("Worker %s has missed %d pings, timing out", workerID, state.PendingPingCount)
		if p.onTimeout != nil {
			p.onTimeout(workerID)
		}
		return
	}

	// Create ping message
	ping := &pb.ControlMessage{
		Message: &pb.ControlMessage_Ping{
			Ping: &pb.Ping{
				Timestamp: time.Now().UnixNano(),
			},
		},
	}

	// Send ping
	if err := p.connectionStore.Send(workerID, ping); err != nil {
		log.Printf("Failed to send ping to worker %s: %v", workerID, err)
		return
	}

	// Update ping state
	state.LastPingTime = time.Now()
	state.PendingPingCount++
	pendingCount := state.PendingPingCount

	log.Printf("Sent ping to worker %s (pending: %d)", workerID, pendingCount)
}

// ResetPingCount resets the pending ping count for a worker (called when pong is received)
func (p *Pinger) ResetPingCount(workerID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if state, exists := p.pingState[workerID]; exists {
		state.PendingPingCount = 0
		log.Printf("Reset ping count for worker %s", workerID)
	}
}

// GetPingState returns the current ping state for a worker
func (p *Pinger) GetPingState(workerID string) (lastPingTime time.Time, pendingCount int, exists bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if state, ok := p.pingState[workerID]; ok {
		return state.LastPingTime, state.PendingPingCount, true
	}
	return time.Time{}, 0, false
}

func (p *Pinger) HandlePongReceived(workerID string, pong *pb.Pong) {
	p.connectionStore.Update(workerID, func(c *Connection) {
		c.UsedCpuCores = pong.UsedCpuCores
		c.UsedMemoryMb = pong.UsedMemoryMb
		c.Containers = pong.Containers
	})
	p.ResetPingCount(workerID)
}
