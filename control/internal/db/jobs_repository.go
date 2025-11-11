package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/mooncorn/dockyard/control/internal/models"
)

// JobRepository defines operations for job persistence
type JobRepository interface {
	CreateJob(job *models.Job) error
	GetJobByID(id string) (*models.Job, error)
	GetPendingJobs() ([]*models.Job, error)
	GetPendingJobsByWorkerID(workerID string) ([]*models.Job, error)
	GetReservedResources(workerID string) (cpuCores float64, memoryMB int64, err error)
	UpdateJobStatus(jobID, status string) error
	UpdateJobCompleted(jobID, result string) error
	UpdateJobFailed(jobID, errorMsg string) error
	GetAllJobs() ([]*models.Job, error)
}

type jobRepository struct {
	db *sqlx.DB
}

// NewJobRepository creates a new job repository
func NewJobRepository(db *sqlx.DB) JobRepository {
	return &jobRepository{db: db}
}

// CreateJob inserts a new job into the database
func (r *jobRepository) CreateJob(job *models.Job) error {
	query := `
		INSERT INTO jobs (id, worker_id, job_type, status, payload, created_at, updated_at)
		VALUES (:id, :worker_id, :job_type, :status, :payload, :created_at, :updated_at)
	`

	_, err := r.db.NamedExec(query, job)
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}

	return nil
}

// GetJobByID retrieves a job by ID
func (r *jobRepository) GetJobByID(id string) (*models.Job, error) {
	var job models.Job
	query := `SELECT * FROM jobs WHERE id = ?`

	err := r.db.Get(&job, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("job not found with id: %s", id)
		}
		return nil, fmt.Errorf("failed to get job by id: %w", err)
	}

	return &job, nil
}

// GetPendingJobs retrieves all pending jobs
func (r *jobRepository) GetPendingJobs() ([]*models.Job, error) {
	var jobs []*models.Job
	query := `SELECT * FROM jobs WHERE status = ? ORDER BY created_at ASC`

	err := r.db.Select(&jobs, query, models.JobStatusPending)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending jobs: %w", err)
	}

	return jobs, nil
}

// GetPendingJobsByWorkerID retrieves pending jobs for a specific worker
func (r *jobRepository) GetPendingJobsByWorkerID(workerID string) ([]*models.Job, error) {
	var jobs []*models.Job
	query := `SELECT * FROM jobs WHERE worker_id = ? AND status = ? ORDER BY created_at ASC`

	err := r.db.Select(&jobs, query, workerID, models.JobStatusPending)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending jobs for worker: %w", err)
	}

	return jobs, nil
}

// GetReservedResources calculates total reserved resources for a worker
// by summing up resource requirements from all pending jobs
func (r *jobRepository) GetReservedResources(workerID string) (cpuCores float64, memoryMB int64, err error) {
	jobs, err := r.GetPendingJobsByWorkerID(workerID)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get pending jobs: %w", err)
	}

	var totalCPU float64
	var totalMemory int64

	for _, job := range jobs {
		cpu, mem, err := job.ExtractResourceRequirements()
		if err != nil {
			// Log error but continue - don't fail the entire calculation
			// TODO: look into this
			continue
		}
		totalCPU += cpu
		totalMemory += mem
	}

	return totalCPU, totalMemory, nil
}

// UpdateJobStatus updates a job's status
func (r *jobRepository) UpdateJobStatus(jobID, status string) error {
	query := `UPDATE jobs SET status = ?, updated_at = ? WHERE id = ?`

	result, err := r.db.Exec(query, status, time.Now(), jobID)
	if err != nil {
		return fmt.Errorf("failed to update job status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("job not found with id: %s", jobID)
	}

	return nil
}

// UpdateJobCompleted updates a job as completed with result
func (r *jobRepository) UpdateJobCompleted(jobID, result string) error {
	query := `
		UPDATE jobs
		SET status = ?, result = ?, completed_at = ?, updated_at = ?
		WHERE id = ?
	`

	now := time.Now()
	_, err := r.db.Exec(query, models.JobStatusCompleted, result, now, now, jobID)
	if err != nil {
		return fmt.Errorf("failed to update job as completed: %w", err)
	}

	return nil
}

// UpdateJobFailed updates a job as failed with error message
func (r *jobRepository) UpdateJobFailed(jobID, errorMsg string) error {
	query := `
		UPDATE jobs
		SET status = ?, error_message = ?, completed_at = ?, updated_at = ?
		WHERE id = ?
	`

	now := time.Now()
	_, err := r.db.Exec(query, models.JobStatusFailed, errorMsg, now, now, jobID)
	if err != nil {
		return fmt.Errorf("failed to update job as failed: %w", err)
	}

	return nil
}

// GetAllJobs retrieves all jobs from the database
func (r *jobRepository) GetAllJobs() ([]*models.Job, error) {
	var jobs []*models.Job
	query := `SELECT * FROM jobs ORDER BY created_at DESC`

	err := r.db.Select(&jobs, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get all jobs: %w", err)
	}

	return jobs, nil
}
