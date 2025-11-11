package services

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mooncorn/dockyard/control/internal/db"
	"github.com/mooncorn/dockyard/control/internal/models"
	"github.com/mooncorn/dockyard/control/internal/worker"
	pb "github.com/mooncorn/dockyard/proto/pb"
)

// WorkerServiceConfig holds the dependencies for WorkerService
type WorkerServiceConfig struct {
	WorkerRegistry *worker.Registry
	WorkerRepo     db.WorkerRepository
	Pinger         *worker.Pinger
}

// WorkerService implements the WorkerService interface
type WorkerService struct {
	workerRegistry *worker.Registry
	workerRepo     db.WorkerRepository
	pinger         *worker.Pinger
}

// NewWorkerService creates a new WorkerService instance
func NewWorkerService(config WorkerServiceConfig) *WorkerService {
	return &WorkerService{
		workerRegistry: config.WorkerRegistry,
		workerRepo:     config.WorkerRepo,
		pinger:         config.Pinger,
	}
}

func (s *WorkerService) CreateWorker() (*models.Worker, error) {
	// Generate worker ID (UUID v4)
	workerID := uuid.New().String()

	// Generate cryptographically secure random token (32 bytes = 64 hex characters)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("failed to generate token: %v", err)
	}

	token := hex.EncodeToString(tokenBytes)

	worker := &models.Worker{
		ID:        workerID,
		Hostname:  "unknown",
		IPAddress: "0.0.0.0",
		CPUCores:  0,
		RAMMB:     0,
		Token:     token,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Create worker in database
	if err := s.workerRepo.CreateWorker(worker); err != nil {
		return nil, fmt.Errorf("failed to create worker: %v", err)
	}

	return worker, nil
}

func (s *WorkerService) ConnectWorker(token string, stream pb.DockyardService_StreamCommunicationServer) (string, error) {
	// Authenticate worker
	worker, err := s.workerRepo.GetWorkerByToken(token)
	if err != nil {
		return "", fmt.Errorf("invalid credentials")
	}

	// Don't override an existing connection with the same token
	if s.workerRegistry.IsOnline(worker.ID) {
		return "", fmt.Errorf("worker %s is already connected", worker.ID)
	}

	// Register worker with the registry
	if err := s.workerRegistry.Register(worker.ID, stream); err != nil {
		return "", fmt.Errorf("failed to register worker with the registry: %s", worker.ID)
	}

	return worker.ID, nil
}

func (s *WorkerService) DisconnectWorker(workerID string) {
	s.workerRegistry.Unregister(workerID)
}

func (s *WorkerService) HandleMetadataUpdate(workerID string, metadata *pb.WorkerMetadata) error {
	// Validate business rules
	if metadata.Hostname == "" {
		return errors.New("hostname cannot be empty")
	}

	if metadata.CpuCores <= 0 {
		return errors.New("cpu cores must be greater than 0")
	}

	if metadata.RamMb <= 0 {
		return errors.New("ram must be greater than 0")
	}

	// Validate data consistency - worker must exist
	_, err := s.workerRepo.GetWorkerByID(workerID)
	if err != nil {
		return fmt.Errorf("worker not found: %w", err)
	}

	// Update worker metadata in database
	return s.workerRepo.UpdateWorkerMetadata(
		workerID,
		metadata.Hostname,
		metadata.IpAddress,
		metadata.CpuCores,
		metadata.RamMb,
	)
}

func (s *WorkerService) HandlePongReceived(workerID string, pong *pb.Pong) {
	s.pinger.HandlePongReceived(workerID, pong)
}
