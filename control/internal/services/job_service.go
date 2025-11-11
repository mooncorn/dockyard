package services

import (
	"fmt"
	"log"

	"github.com/mooncorn/dockyard/control/internal/db"
	"github.com/mooncorn/dockyard/control/internal/models"
	pb "github.com/mooncorn/dockyard/proto/pb"
)

type JobService struct {
	jobRepo db.JobRepository
}

func NewJobService(jobRepo db.JobRepository) *JobService {
	return &JobService{
		jobRepo: jobRepo,
	}
}

// HandleJobResponse processes job responses received from workers
func (s *JobService) HandleJobResponse(jobResponse *pb.JobResponse) error {
	if jobResponse == nil {
		return fmt.Errorf("job response is nil")
	}

	jobID := jobResponse.JobId
	if jobID == "" {
		return fmt.Errorf("job response missing job_id")
	}

	// Retrieve the job from database
	job, err := s.jobRepo.GetJobByID(jobID)
	if err != nil {
		return fmt.Errorf("failed to retrieve job %s: %w", jobID, err)
	}

	// Validate job is in pending status
	if job.Status != models.JobStatusPending {
		log.Printf("Warning: received response for job %s with status %s (expected pending)", jobID, job.Status)
		// Don't return error - response was already processed
		return nil
	}

	// Process based on success/failure
	if jobResponse.Success {
		// Job completed successfully
		result := jobResponse.ContainerId
		if result == "" {
			return fmt.Errorf("missing container_id in job response %s", jobID)
		}

		err = s.jobRepo.UpdateJobCompleted(jobID, result)
		if err != nil {
			return fmt.Errorf("failed to mark job %s as completed: %w", jobID, err)
		}

		log.Printf("Job %s completed successfully with result: %s", jobID, result)
	} else {
		// Job failed
		errorMsg := jobResponse.ErrorMessage
		if errorMsg == "" {
			return fmt.Errorf("missing error_msg in job response: %s", jobID)
		}

		err = s.jobRepo.UpdateJobFailed(jobID, errorMsg)
		if err != nil {
			return fmt.Errorf("failed to mark job %s as failed: %w", jobID, err)
		}

		log.Printf("Job %s failed with error: %s", jobID, errorMsg)
	}

	return nil
}

// HandlePongReceived checks containers in pong messages and auto-completes pending jobs
// when their containers are detected as running
func (s *JobService) HandlePongReceived(workerID string, pong *pb.Pong) error {
	if pong == nil {
		return fmt.Errorf("pong message is nil")
	}

	// Get all pending jobs for this worker
	pendingJobs, err := s.jobRepo.GetPendingJobsByWorkerID(workerID)
	if err != nil {
		return fmt.Errorf("failed to get pending jobs for worker %s: %w", workerID, err)
	}

	// If no pending jobs, nothing to check
	if len(pendingJobs) == 0 {
		return nil
	}

	// Create a map of pending jobs by ID for fast lookup
	pendingJobsMap := make(map[string]*models.Job)
	for _, job := range pendingJobs {
		pendingJobsMap[job.ID] = job
	}

	// Check each container in the pong message
	for _, container := range pong.Containers {
		// Only consider containers that are running or created
		if container.State != "running" && container.State != "created" {
			continue
		}

		// Check if this container's job_id matches a pending job
		jobID := container.JobId
		if jobID == "" {
			continue
		}

		_, exists := pendingJobsMap[jobID]
		if !exists {
			continue
		}

		// Found a match - mark the job as completed with this container_id
		containerID := container.ContainerId
		err = s.jobRepo.UpdateJobCompleted(jobID, containerID)
		if err != nil {
			log.Printf("Failed to auto-complete job %s from pong (container %s): %v", jobID, containerID, err)
			continue
		}

		log.Printf("Auto-completed job %s from pong message (container %s, state: %s)", jobID, containerID, container.State)

		// Remove from map so we don't try to complete it again in this pong
		delete(pendingJobsMap, jobID)
	}

	return nil
}
