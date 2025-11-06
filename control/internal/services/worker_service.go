package services

import (
	"errors"
	"fmt"

	"github.com/mooncorn/dockyard/control/internal/db"
	"github.com/mooncorn/dockyard/control/internal/models"
	"github.com/mooncorn/dockyard/control/internal/registry"
	pb "github.com/mooncorn/dockyard/proto/pb"
)

// WorkerService defines the interface for worker-related business logic
type WorkerService interface {
	// CreateWorker validates and creates a new worker
	CreateWorker(worker *models.Worker) error

	// RegisterWorker registers a worker connection with the given stream
	RegisterWorker(workerID string, stream pb.DockyardService_StreamCommunicationServer) error

	// UnregisterWorker removes a worker connection
	UnregisterWorker(workerID string)

	// HandlePongReceived processes a pong message from a worker
	HandlePongReceived(workerID string, pong *pb.Pong)

	// HandleMetadataUpdate validates and updates worker metadata
	HandleMetadataUpdate(workerID string, metadata *pb.WorkerMetadata) error

	// GetWorkerWithStatus retrieves a worker with its current status
	GetWorkerWithStatus(workerID string) (*models.WorkerWithStatus, error)

	// ListWorkersWithStatus retrieves all workers with their current status
	ListWorkersWithStatus() ([]*models.WorkerWithStatus, error)

	// IsWorkerOnline checks if a worker is currently online
	IsWorkerOnline(workerID string) bool
}

// WorkerServiceConfig holds the dependencies for WorkerService
type WorkerServiceConfig struct {
	WorkerRegistry registry.WorkerRegistry
	WorkerRepo     db.WorkerRepository
}

// workerService implements the WorkerService interface
type workerService struct {
	workerRegistry registry.WorkerRegistry
	workerRepo     db.WorkerRepository
}

// NewWorkerService creates a new WorkerService instance
func NewWorkerService(config WorkerServiceConfig) WorkerService {
	return &workerService{
		workerRegistry: config.WorkerRegistry,
		workerRepo:     config.WorkerRepo,
	}
}

// CreateWorker validates and creates a new worker
func (s *workerService) CreateWorker(worker *models.Worker) error {
	// Validate required fields
	if worker.ID == "" {
		return errors.New("worker ID cannot be empty")
	}

	if worker.Token == "" {
		return errors.New("worker token cannot be empty")
	}

	// Create worker in database
	return s.workerRepo.CreateWorker(worker)
}

// RegisterWorker registers a worker connection with the given stream
func (s *workerService) RegisterWorker(workerID string, stream pb.DockyardService_StreamCommunicationServer) error {
	return s.workerRegistry.RegisterWorker(workerID, stream)
}

// UnregisterWorker removes a worker connection
func (s *workerService) UnregisterWorker(workerID string) {
	s.workerRegistry.UnregisterWorker(workerID)
}

// HandlePongReceived processes a pong message from a worker
func (s *workerService) HandlePongReceived(workerID string, pong *pb.Pong) {
	s.workerRegistry.HandlePongReceived(workerID, pong)
}

// HandleMetadataUpdate validates and updates worker metadata
func (s *workerService) HandleMetadataUpdate(workerID string, metadata *pb.WorkerMetadata) error {
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
		int(metadata.CpuCores),
		int(metadata.RamMb),
	)
}

// GetWorkerWithStatus retrieves a worker with its current status
func (s *workerService) GetWorkerWithStatus(workerID string) (*models.WorkerWithStatus, error) {
	// Fetch worker from database
	worker, err := s.workerRepo.GetWorkerByID(workerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get worker: %w", err)
	}

	// Get in-memory connection status from registry
	connection, isConnected := s.workerRegistry.GetWorker(workerID)

	// Create WorkerWithStatus combining both sources
	workerWithStatus := &models.WorkerWithStatus{
		ID:        worker.ID,
		Hostname:  worker.Hostname,
		IPAddress: worker.IPAddress,
		CPUCores:  worker.CPUCores,
		RAMMB:     worker.RAMMB,
		CreatedAt: worker.CreatedAt,
		UpdatedAt: worker.UpdatedAt,
		Status:    "offline", // Default to offline
	}

	// If worker is connected and online, add real-time status and health metrics
	if isConnected && connection.Status == registry.WorkerStatusOnline {
		workerWithStatus.Status = "online"
		workerWithStatus.LastPingTime = &connection.LastPingTime
		workerWithStatus.PendingPingCount = &connection.PendingPingCount
	}

	return workerWithStatus, nil
}

// ListWorkersWithStatus retrieves all workers with their current status
func (s *workerService) ListWorkersWithStatus() ([]*models.WorkerWithStatus, error) {
	// Get all workers from database
	workers, err := s.workerRepo.GetAllWorkers()
	if err != nil {
		return nil, fmt.Errorf("failed to get workers: %w", err)
	}

	// Combine with status from registry
	workersWithStatus := make([]*models.WorkerWithStatus, 0, len(workers))
	for _, worker := range workers {
		status := "offline"
		if s.workerRegistry.IsWorkerOnline(worker.ID) {
			status = "online"
		}

		workersWithStatus = append(workersWithStatus, &models.WorkerWithStatus{
			ID:        worker.ID,
			Hostname:  worker.Hostname,
			IPAddress: worker.IPAddress,
			CPUCores:  worker.CPUCores,
			RAMMB:     worker.RAMMB,
			CreatedAt: worker.CreatedAt,
			UpdatedAt: worker.UpdatedAt,
			Status:    status,
		})
	}

	return workersWithStatus, nil
}

// IsWorkerOnline checks if a worker is currently online
func (s *workerService) IsWorkerOnline(workerID string) bool {
	return s.workerRegistry.IsWorkerOnline(workerID)
}
