package job

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mooncorn/dockyard/control/internal/db"
	"github.com/mooncorn/dockyard/control/internal/models"
	"github.com/mooncorn/dockyard/control/internal/worker"
	pb "github.com/mooncorn/dockyard/proto/pb"
)

// SchedulerConfig holds configuration for the scheduler
type SchedulerConfig struct {
	JobRepo         db.JobRepository
	ConnectionStore *worker.ConnectionStore
	PollInterval    time.Duration // How often to check for pending jobs
}

// Scheduler picks up pending jobs and sends them to assigned workers
type Scheduler struct {
	jobRepo         db.JobRepository
	connectionStore *worker.ConnectionStore
	pollInterval    time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
}

// NewScheduler creates a new scheduler instance
func NewScheduler(config SchedulerConfig) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())

	pollInterval := config.PollInterval
	if pollInterval == 0 {
		pollInterval = 30 * time.Second // Default to 30 seconds
	}

	return &Scheduler{
		jobRepo:         config.JobRepo,
		connectionStore: config.ConnectionStore,
		pollInterval:    pollInterval,
		ctx:             ctx,
		cancel:          cancel,
	}
}

// Start begins the scheduler's polling loop
func (s *Scheduler) Start() {
	log.Println("Scheduler started")

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	// Run immediately on start, then on each tick
	s.processPendingJobs()

	for {
		select {
		case <-ticker.C:
			s.processPendingJobs()
		case <-s.ctx.Done():
			log.Println("Scheduler stopped")
			return
		}
	}
}

// Stop gracefully stops the scheduler
func (s *Scheduler) Stop() {
	log.Println("Stopping scheduler...")
	s.cancel()
}

// processPendingJobs queries for pending jobs and sends them to workers
func (s *Scheduler) processPendingJobs() {
	// Get all pending jobs
	jobs, err := s.jobRepo.GetPendingJobs()
	if err != nil {
		log.Printf("Failed to get pending jobs: %v", err)
		return
	}

	if len(jobs) == 0 {
		return
	}

	log.Printf("Processing %d pending jobs", len(jobs))

	for _, job := range jobs {
		if err := s.sendJobToWorker(job); err != nil {
			log.Printf("Failed to send job %s to worker %s: %v", job.ID, job.WorkerID, err)
			// Continue processing other jobs
		} else {
			log.Printf("Successfully sent job %s to worker %s", job.ID, job.WorkerID)
		}
	}
}

// sendJobToWorker sends a job to its assigned worker
func (s *Scheduler) sendJobToWorker(job *models.Job) error {
	// Check if worker is online
	connection, exists := s.connectionStore.Get(job.WorkerID)
	if !exists {
		return fmt.Errorf("worker %s is not online", job.WorkerID)
	}

	if connection.Status != worker.StatusOnline {
		return fmt.Errorf("worker %s is not online (status: %v)", job.WorkerID, connection.Status)
	}

	// Build message based on job type
	var controlMessage *pb.ControlMessage
	switch job.JobType {
	case models.JobTypeCreateContainer:
		var req models.CreateContainerRequest
		if err := job.GetPayloadAs(&req); err != nil {
			return fmt.Errorf("failed to unmarshal job payload: %w", err)
		}

		controlMessage = &pb.ControlMessage{
			Message: &pb.ControlMessage_JobRequest{
				JobRequest: &pb.JobRequest{
					Payload: &pb.JobRequest_CreateContainer{
						CreateContainer: &pb.CreateContainerJob{
							JobId:          job.ID,
							Image:          req.Image,
							Env:            req.Env,
							ContainerPorts: req.Ports,
							VolumeTargets:  req.Volumes,
							MemoryMb:       req.MemoryMB,
							CpuCores:       req.CPUCores,
						},
					},
				},
			},
		}
	default:
		return fmt.Errorf("unknown job type: %s", job.JobType)
	}

	// Send message to worker
	if err := s.connectionStore.Send(job.WorkerID, controlMessage); err != nil {
		return fmt.Errorf("failed to send message to worker: %w", err)
	}

	return nil
}
