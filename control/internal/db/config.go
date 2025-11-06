package db

import (
	"fmt"
	"log"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// Config holds database configuration
type Config struct {
	DatabasePath   string
	MigrationsPath string
}

// NewDB creates a new database connection
func NewDB(cfg Config) (*sqlx.DB, error) {
	db, err := sqlx.Connect("sqlite", cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(1) // SQLite works best with single connection
	db.SetMaxIdleConns(1)

	return db, nil
}

// RunMigrations runs database migrations
func RunMigrations(cfg Config) error {
	dbURL := fmt.Sprintf("sqlite://%s", cfg.DatabasePath)
	migrationsURL := fmt.Sprintf("file://%s", cfg.MigrationsPath)

	m, err := migrate.New(migrationsURL, dbURL)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Println("Database migrations completed successfully")
	return nil
}
