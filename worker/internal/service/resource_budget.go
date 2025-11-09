package service

import (
	"fmt"
	"runtime"

	"github.com/shirou/gopsutil/v3/mem"
)

// ResourceBudget represents the CPU and memory budget allocated to the worker
type ResourceBudget struct {
	TotalCPUCores float64
	TotalMemoryMB int64
	IsAutoReserve bool
}

// NewResourceBudget creates a new ResourceBudget
// If cpuCores or memoryMB are 0 and autoReserve is true, it will auto-detect system resources
// and reserve 80% for the worker
func NewResourceBudget(cpuCores float64, memoryMB int64, autoReserve bool) (*ResourceBudget, error) {
	budget := &ResourceBudget{
		TotalCPUCores: cpuCores,
		TotalMemoryMB: memoryMB,
		IsAutoReserve: autoReserve,
	}

	// Auto-reserve resources if requested and values are not manually set
	if autoReserve && (cpuCores == 0 || memoryMB == 0) {
		if err := budget.autoDetectResources(); err != nil {
			return nil, fmt.Errorf("failed to auto-detect resources: %w", err)
		}
	}

	// Validate that we have valid budget values
	if budget.TotalCPUCores <= 0 {
		return nil, fmt.Errorf("invalid CPU budget: must be greater than 0")
	}
	if budget.TotalMemoryMB <= 0 {
		return nil, fmt.Errorf("invalid memory budget: must be greater than 0")
	}

	return budget, nil
}

// autoDetectResources detects system resources and sets budget to 80%
func (b *ResourceBudget) autoDetectResources() error {
	// Detect CPU cores
	if b.TotalCPUCores == 0 {
		numCPU := runtime.NumCPU()
		// Reserve 80% of available CPU cores
		b.TotalCPUCores = float64(numCPU) * 0.8
	}

	// Detect memory
	if b.TotalMemoryMB == 0 {
		vmStat, err := mem.VirtualMemory()
		if err != nil {
			return fmt.Errorf("failed to get memory info: %w", err)
		}
		// Reserve 80% of total memory in MB
		totalMemoryMB := int64(vmStat.Total / (1024 * 1024))
		b.TotalMemoryMB = int64(float64(totalMemoryMB) * 0.8)
	}

	return nil
}

// GetAvailableCPU returns the total available CPU cores
func (b *ResourceBudget) GetAvailableCPU() float64 {
	return b.TotalCPUCores
}

// GetAvailableMemory returns the total available memory in MB
func (b *ResourceBudget) GetAvailableMemory() int64 {
	return b.TotalMemoryMB
}

// CanAllocate checks if the requested resources can be allocated
// given the current usage
func (b *ResourceBudget) CanAllocate(requestedCPU float64, requestedMemoryMB int64, usedCPU float64, usedMemoryMB int64) bool {
	availableCPU := b.TotalCPUCores - usedCPU
	availableMemory := b.TotalMemoryMB - usedMemoryMB

	return requestedCPU <= availableCPU && requestedMemoryMB <= availableMemory
}

// String returns a string representation of the budget
func (b *ResourceBudget) String() string {
	return fmt.Sprintf("ResourceBudget{CPU: %.2f cores, Memory: %d MB, AutoReserve: %v}",
		b.TotalCPUCores, b.TotalMemoryMB, b.IsAutoReserve)
}
