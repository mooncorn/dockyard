package services

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mooncorn/dockyard/control/internal/db"
	"github.com/mooncorn/dockyard/control/internal/models"
	"github.com/mooncorn/dockyard/control/internal/worker"
)

type ContainerServiceConfig struct {
	ConnectionStore *worker.ConnectionStore
	WorkerRepo      db.WorkerRepository
	JobRepo         db.JobRepository
}

type ContainerService struct {
	selectionMu     sync.Mutex // Protects worker selection and job creation from race conditions
	connectionStore *worker.ConnectionStore
	workerRepo      db.WorkerRepository
	jobRepo         db.JobRepository
}

func NewContainerService(config ContainerServiceConfig) *ContainerService {
	return &ContainerService{
		connectionStore: config.ConnectionStore,
		workerRepo:      config.WorkerRepo,
		jobRepo:         config.JobRepo,
	}
}

func (c *ContainerService) CreateContainer(req *models.CreateContainerRequest) (string, error) {
	// Validate request
	if req.Image == "" {
		return "", fmt.Errorf("image is required")
	}
	if req.CPUCores <= 0 {
		return "", fmt.Errorf("cpu_cores must be greater than 0")
	}
	if req.MemoryMB <= 0 {
		return "", fmt.Errorf("memory_mb must be greater than 0")
	}

	// Validate environment variables format (KEY=VALUE)
	for _, env := range req.Env {
		if len(env) == 0 {
			return "", fmt.Errorf("environment variable cannot be empty")
		}
	}

	// Validate ports are positive
	for _, port := range req.Ports {
		if port <= 0 {
			return "", fmt.Errorf("port must be greater than 0")
		}
	}

	// Lock to prevent concurrent worker selection and job creation race conditions
	// This ensures the read (resource check) and write (job creation) are atomic
	c.selectionMu.Lock()
	defer c.selectionMu.Unlock()

	// Select worker with sufficient resources
	workerID, err := c.selectWorker(req.CPUCores, req.MemoryMB)
	if err != nil {
		return "", fmt.Errorf("failed to select worker: %w", err)
	}

	log.Printf("[ContainerService] Selected worker %s for container job (CPU: %.2f, Memory: %d MB)",
		workerID, req.CPUCores, req.MemoryMB)

	// Create job record
	job := &models.Job{
		ID:        uuid.New().String(),
		WorkerID:  workerID,
		JobType:   models.JobTypeCreateContainer,
		Status:    models.JobStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Set payload
	if err := job.SetPayload(req); err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Persist job to database
	if err := c.jobRepo.CreateJob(job); err != nil {
		return "", fmt.Errorf("failed to create job: %w", err)
	}

	// Job is now pending - scheduler will pick it up and send to worker
	return job.ID, nil
}

// SelectWorker takes in CPU and Memory needed to run a container and selects a worker with available resources.
// It selects the worker with the most available resources (spread load strategy).
func (c *ContainerService) selectWorker(cpu float64, memory int64) (string, error) {
	// Get all online worker IDs from connection store
	onlineConnections := c.connectionStore.GetAll()
	if len(onlineConnections) == 0 {
		return "", fmt.Errorf("no workers online")
	}

	// Fetch all workers from database to get resource budgets
	allWorkers, err := c.workerRepo.GetAllWorkers()
	if err != nil {
		return "", fmt.Errorf("failed to get workers: %w", err)
	}

	// Create a map for quick lookup
	workerMap := make(map[string]*models.Worker)
	for _, w := range allWorkers {
		workerMap[w.ID] = w
	}

	// Track the best candidate
	var bestWorkerID string
	var maxAvailableCPU float64

	// Evaluate each online worker
	for workerID, connection := range onlineConnections {
		worker, exists := workerMap[workerID]
		if !exists {
			continue // Skip if worker not in database
		}

		// Check channel capacity for backpressure - skip workers with overloaded queues
		channelUsed, channelCapacity, err := c.connectionStore.GetChannelUtilization(workerID)
		if err != nil {
			log.Printf("[ContainerService] Failed to get channel utilization for worker %s: %v", workerID, err)
			continue
		}
		// Skip workers with >90% channel utilization to prevent channel overflow
		utilizationPercent := float64(channelUsed) / float64(channelCapacity) * 100
		if utilizationPercent > 90 {
			log.Printf("[ContainerService] Skipping worker %s due to high channel utilization: %.1f%% (%d/%d)",
				workerID, utilizationPercent, channelUsed, channelCapacity)
			continue
		}

		// Get resource budget from database
		budgetCPU := worker.CPUCores
		budgetMemory := worker.RAMMB

		// Get used resources from connection (updated by pinger)
		usedCPU := connection.UsedCpuCores
		usedMemory := connection.UsedMemoryMb

		// Get reserved resources from pending jobs
		reservedCPU, reservedMemory, err := c.jobRepo.GetReservedResources(workerID)
		if err != nil {
			// Log error but continue - don't fail the entire selection
			log.Printf("[ContainerService] Failed to get reserved resources for worker %s: %v", workerID, err)
			continue
		}

		// Calculate available resources
		availableCPU := budgetCPU - usedCPU - reservedCPU
		availableMemory := budgetMemory - usedMemory - reservedMemory

		// Check if worker has sufficient resources
		if availableCPU >= cpu && availableMemory >= memory {
			// Select worker with most available resources
			if bestWorkerID == "" || availableCPU > maxAvailableCPU {
				bestWorkerID = workerID
				maxAvailableCPU = availableCPU
				log.Printf("[ContainerService] Candidate worker %s: CPU available=%.2f, Memory available=%d MB, Channel=%d/%d",
					workerID, availableCPU, availableMemory, channelUsed, channelCapacity)
			}
		}
	}

	if bestWorkerID == "" {
		log.Printf("[ContainerService] No suitable worker found for request (CPU: %.2f, Memory: %d MB). "+
			"Checked %d online workers. All workers either lack resources, have high channel utilization, or are offline.",
			cpu, memory, len(onlineConnections))
		return "", fmt.Errorf("no suitable worker found with sufficient resources")
	}

	log.Printf("[ContainerService] Final worker selection: %s (Max available CPU: %.2f)", bestWorkerID, maxAvailableCPU)
	return bestWorkerID, nil
}
