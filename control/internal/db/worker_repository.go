package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/mooncorn/dockyard/control/internal/models"
)

// WorkerRepository defines operations for worker persistence
type WorkerRepository interface {
	CreateWorker(worker *models.Worker) error
	GetWorkerByToken(token string) (*models.Worker, error)
	GetWorkerByID(id string) (*models.Worker, error)
	UpdateWorkerMetadata(id, hostname, ipAddress string, cpuCores float64, ramMB int64) error
	GetAllWorkers() ([]*models.Worker, error)
}

type workerRepository struct {
	db *sqlx.DB
}

// NewWorkerRepository creates a new worker repository
func NewWorkerRepository(db *sqlx.DB) WorkerRepository {
	return &workerRepository{db: db}
}

// CreateWorker inserts a new worker into the database
func (r *workerRepository) CreateWorker(worker *models.Worker) error {
	query := `
		INSERT INTO workers (id, hostname, ip_address, cpu_cores, ram_mb, token, created_at, updated_at)
		VALUES (:id, :hostname, :ip_address, :cpu_cores, :ram_mb, :token, :created_at, :updated_at)
	`

	_, err := r.db.NamedExec(query, worker)
	if err != nil {
		return fmt.Errorf("failed to create worker: %w", err)
	}

	return nil
}

// GetWorkerByToken retrieves a worker by their access token
func (r *workerRepository) GetWorkerByToken(token string) (*models.Worker, error) {
	var worker models.Worker
	query := `SELECT * FROM workers WHERE token = ?`

	err := r.db.Get(&worker, query, token)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("worker not found with provided token")
		}
		return nil, fmt.Errorf("failed to get worker by token: %w", err)
	}

	return &worker, nil
}

// GetWorkerByID retrieves a worker by their ID
func (r *workerRepository) GetWorkerByID(id string) (*models.Worker, error) {
	var worker models.Worker
	query := `SELECT * FROM workers WHERE id = ?`

	err := r.db.Get(&worker, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("worker not found with id: %s", id)
		}
		return nil, fmt.Errorf("failed to get worker by id: %w", err)
	}

	return &worker, nil
}

// UpdateWorkerMetadata updates worker's system specifications
func (r *workerRepository) UpdateWorkerMetadata(id, hostname, ipAddress string, cpuCores float64, ramMB int64) error {
	query := `
		UPDATE workers
		SET hostname = ?, ip_address = ?, cpu_cores = ?, ram_mb = ?, updated_at = ?
		WHERE id = ?
	`

	result, err := r.db.Exec(query, hostname, ipAddress, cpuCores, ramMB, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update worker metadata: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("worker not found with id: %s", id)
	}

	return nil
}

// GetAllWorkers retrieves all workers from the database
func (r *workerRepository) GetAllWorkers() ([]*models.Worker, error) {
	var workers []*models.Worker
	query := `SELECT * FROM workers ORDER BY created_at DESC`

	err := r.db.Select(&workers, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get all workers: %w", err)
	}

	return workers, nil
}
