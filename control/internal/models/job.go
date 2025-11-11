package models

import (
	"encoding/json"
	"fmt"
	"time"
)

// Job statuses
const (
	JobStatusPending    = "pending"
	JobStatusDispatched = "dispatched" // Job has been sent to worker but not yet completed
	JobStatusCompleted  = "completed"
	JobStatusFailed     = "failed"
)

// Job types
const (
	JobTypeCreateContainer = "create_container"
)

// CreateContainerRequest represents a request to create a container
type CreateContainerRequest struct {
	Image    string   `json:"image" validate:"required"`
	Env      []string `json:"env"`     // container env in the format of KEY=VALUE
	Ports    []int32  `json:"ports"`   // internal container ports
	Volumes  []string `json:"volumes"` // internal container volumes
	CPUCores float64  `json:"cpu_cores" validate:"required,gt=0"`
	MemoryMB int64    `json:"memory_mb" validate:"required,gt=0"`
}

// Job represents a generic job in the system
type Job struct {
	ID           string     `db:"id" json:"id"`
	WorkerID     string     `db:"worker_id" json:"worker_id"`
	JobType      string     `db:"job_type" json:"job_type"`
	Status       string     `db:"status" json:"status"`
	Payload      string     `db:"payload" json:"payload"`
	Result       *string    `db:"result" json:"result,omitempty"`
	ErrorMessage *string    `db:"error_message" json:"error_message,omitempty"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at" json:"updated_at"`
	CompletedAt  *time.Time `db:"completed_at" json:"completed_at,omitempty"`
}

// GetPayloadAs unmarshals the job payload into the provided struct
func (j *Job) GetPayloadAs(v interface{}) error {
	return json.Unmarshal([]byte(j.Payload), v)
}

// SetPayload marshals the provided struct into the job payload
func (j *Job) SetPayload(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	j.Payload = string(data)
	return nil
}

// GetResultAs unmarshals the job result into the provided struct
func (j *Job) GetResultAs(v interface{}) error {
	if j.Result == nil {
		return fmt.Errorf("result is nil")
	}
	return json.Unmarshal([]byte(*j.Result), v)
}

// SetResult marshals the provided struct into the job result
func (j *Job) SetResult(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	result := string(data)
	j.Result = &result
	return nil
}

// ExtractResourceRequirements extracts CPU and memory requirements from job payload
func (j *Job) ExtractResourceRequirements() (cpuCores float64, memoryMB int64, err error) {
	switch j.JobType {
	case JobTypeCreateContainer:
		var req CreateContainerRequest
		if err := j.GetPayloadAs(&req); err != nil {
			return 0, 0, err
		}
		return req.CPUCores, req.MemoryMB, nil
	default:
		return 0, 0, fmt.Errorf("unknown job type: %s", j.JobType)
	}
}
