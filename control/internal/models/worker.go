package models

import "time"

// Worker represents a worker in the database
type Worker struct {
	ID         string    `db:"id" json:"id"`
	Hostname   string    `db:"hostname" json:"hostname"`
	IPAddress  string    `db:"ip_address" json:"ip_address"`
	CPUCores   int       `db:"cpu_cores" json:"cpu_cores"`
	RAMMB      int       `db:"ram_mb" json:"ram_mb"`
	Token      string    `db:"token" json:"-"` // Never expose token in JSON
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time `db:"updated_at" json:"updated_at"`
}

// WorkerWithStatus combines database worker data with in-memory status
type WorkerWithStatus struct {
	ID               string     `json:"id"`
	Hostname         string     `json:"hostname"`
	IPAddress        string     `json:"ip_address"`
	CPUCores         int        `json:"cpu_cores"`
	RAMMB            int        `json:"ram_mb"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Status           string     `json:"status"`              // online/offline
	LastPingTime     *time.Time `json:"last_ping_time,omitempty"`
	PendingPingCount *int       `json:"pending_ping_count,omitempty"`
}
