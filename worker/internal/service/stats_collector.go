package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/mooncorn/dockyard/proto/pb"
)

// StatsCollector defines the interface for collecting container statistics
type StatsCollector interface {
	// CollectStats collects statistics for all containers
	// Returns: container stats, total used CPU cores, total used memory MB, error
	CollectStats(ctx context.Context) ([]*pb.ContainerStats, float64, int64, error)
}

// dockerStatsCollector implements StatsCollector using Docker client
type dockerStatsCollector struct {
	client          *client.Client
	containerJobMap map[string]string // containerID -> jobID mapping
}

// NewStatsCollector creates a new StatsCollector instance
func NewStatsCollector(dockerClient *client.Client, containerJobMap map[string]string) StatsCollector {
	return &dockerStatsCollector{
		client:          dockerClient,
		containerJobMap: containerJobMap,
	}
}

// CollectStats collects statistics for all running containers
func (s *dockerStatsCollector) CollectStats(ctx context.Context) ([]*pb.ContainerStats, float64, int64, error) {
	// List all containers (including stopped ones for state tracking)
	containers, err := s.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to list containers: %w", err)
	}

	var stats []*pb.ContainerStats
	var totalCPUCores float64
	var totalMemoryMB int64

	for _, c := range containers {
		// Get container name (remove leading slash)
		name := ""
		if len(c.Names) > 0 {
			name = c.Names[0]
			if len(name) > 0 && name[0] == '/' {
				name = name[1:]
			}
		}

		// Get job ID from mapping
		jobID := s.containerJobMap[c.ID]

		// For running containers, collect real-time stats
		var cpuUsage float64
		var memoryUsageMB int64

		if c.State == "running" {
			cpuUsage, memoryUsageMB, err = s.getContainerStats(ctx, c.ID)
			if err != nil {
				// Log error but continue with other containers
				log.Printf("Failed to get stats for container %s: %v", c.ID, err)
				// Set to 0 if we can't get stats
				cpuUsage = 0
				memoryUsageMB = 0
			}

			// Add to totals
			totalCPUCores += cpuUsage
			totalMemoryMB += memoryUsageMB
		}

		// Add container stats
		stats = append(stats, &pb.ContainerStats{
			ContainerId:   c.ID[:12], // Use short ID
			JobId:         jobID,
			Name:          name,
			State:         c.State,
			CpuUsageCores: cpuUsage,
			MemoryUsageMb: memoryUsageMB,
		})
	}

	return stats, totalCPUCores, totalMemoryMB, nil
}

// getContainerStats retrieves CPU and memory stats for a single container
func (s *dockerStatsCollector) getContainerStats(ctx context.Context, containerID string) (float64, int64, error) {
	// Get container stats (stream=false to get a single snapshot)
	stats, err := s.client.ContainerStats(ctx, containerID, false)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get container stats: %w", err)
	}
	defer stats.Body.Close()

	// Read and parse stats
	var v container.StatsResponse
	if err := json.NewDecoder(stats.Body).Decode(&v); err != nil {
		if err == io.EOF {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("failed to decode stats: %w", err)
	}

	// Calculate CPU usage as a percentage of available cores
	cpuCores := calculateCPUCores(&v)

	// Get memory usage in MB
	memoryMB := int64(v.MemoryStats.Usage / (1024 * 1024))

	return cpuCores, memoryMB, nil
}

// calculateCPUCores calculates the number of CPU cores being used
// This is based on the CPU usage percentage across all available cores
func calculateCPUCores(stats *container.StatsResponse) float64 {
	// Calculate CPU delta
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage) - float64(stats.PreCPUStats.CPUUsage.TotalUsage)

	// Calculate system CPU delta
	systemDelta := float64(stats.CPUStats.SystemUsage) - float64(stats.PreCPUStats.SystemUsage)

	// Get number of CPUs
	numCPUs := float64(stats.CPUStats.OnlineCPUs)
	if numCPUs == 0 {
		// Fallback to counting CPUs in usage
		numCPUs = float64(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}
	if numCPUs == 0 {
		numCPUs = 1 // Fallback to 1 if we can't determine
	}

	// Calculate CPU usage as a fraction of available cores
	if systemDelta > 0 && cpuDelta > 0 {
		cpuPercent := (cpuDelta / systemDelta) * numCPUs
		return cpuPercent
	}

	return 0
}
