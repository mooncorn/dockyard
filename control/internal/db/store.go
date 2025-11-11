package db

import "github.com/jmoiron/sqlx"

// Store provides access to all repositories
type Store struct {
	db         *sqlx.DB
	WorkerRepo WorkerRepository
	JobRepo    JobRepository
}

// NewStore creates a new store with all repositories
func NewStore(db *sqlx.DB) *Store {
	return &Store{
		db:         db,
		WorkerRepo: NewWorkerRepository(db),
		JobRepo:    NewJobRepository(db),
	}
}

// DB returns the underlying database connection
func (s *Store) DB() *sqlx.DB {
	return s.db
}
