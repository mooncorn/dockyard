package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// ContainerInfo represents detailed information about a container
type ContainerInfo struct {
	ID              string
	Name            string
	State           string // "running", "exited", "created", etc
	Image           string
	Ports           []PortMapping
	IsManaged       bool     // tracked by this service
	AssignedPorts   []int    // ports allocated by this service
	AssignedVolumes []string // volumes allocated by this service
}

// PortMapping represents a port mapping between container and host
type PortMapping struct {
	ContainerPort int
	HostPort      int
	Protocol      string // "tcp" or "udp"
}

// DockerService defines the interface for Docker container management
type DockerService interface {
	// Container lifecycle
	CreateContainer(ctx context.Context, config ContainerConfig) (string, error)
	Start(ctx context.Context, containerID string) error
	Stop(ctx context.Context, containerID string, timeout *int) error
	Restart(ctx context.Context, containerID string, timeout *int) error
	Remove(ctx context.Context, containerID string, force bool) error

	// Advanced operations
	GetLogs(ctx context.Context, containerID string, tail string, follow bool) (io.ReadCloser, error)
	Exec(ctx context.Context, containerID string, cmd []string) (string, error)
	InspectContainer(ctx context.Context, containerID string) (*container.InspectResponse, error)
	ListContainers(ctx context.Context, runningOnly bool) ([]ContainerInfo, error)

	// Resource management
	GetAssignedPorts(containerID string) []int
	GetAssignedVolumes(containerID string) []string

	// Cleanup
	Close() error
}

// PortRange represents a range of available ports
type PortRange struct {
	Min int
	Max int
}

// DockerServiceConfig holds configuration for the Docker service
type DockerServiceConfig struct {
	BaseVolumePath string
	PortRange      *PortRange
	AvailablePorts []int
}

// ContainerConfig represents configuration for creating a container
type ContainerConfig struct {
	Image         string
	Name          string
	Cmd           []string
	Env           []string
	Volumes       []VolumeConfig
	Ports         []PortConfig
	CPULimit      int64  // CPU cores * 1e9 (e.g., 1.5 cores = 1500000000)
	MemoryLimit   int64  // Memory in bytes
	RestartPolicy string // "no", "always", "on-failure", "unless-stopped"
}

// VolumeConfig represents a volume mount configuration
type VolumeConfig struct {
	Name     string
	HostPath string // If empty, will be auto-generated
	Target   string // Container path
	ReadOnly bool
}

// PortConfig represents a port mapping configuration
type PortConfig struct {
	ContainerPort int
	HostPort      int    // If 0, will be auto-assigned
	Protocol      string // "tcp" or "udp", defaults to "tcp"
}

// dockerService implements the DockerService interface
type dockerService struct {
	client          *client.Client
	config          DockerServiceConfig
	mu              sync.RWMutex
	assignedPorts   map[string][]int    // containerID -> assigned ports
	assignedVolumes map[string][]string // containerID -> volume paths
	availablePorts  []int
	portInUse       map[int]bool
}

// NewDockerService creates a new Docker service instance
func NewDockerService(config DockerServiceConfig) (DockerService, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Initialize available ports
	availablePorts := config.AvailablePorts
	if availablePorts == nil && config.PortRange != nil {
		availablePorts = make([]int, 0, config.PortRange.Max-config.PortRange.Min+1)
		for p := config.PortRange.Min; p <= config.PortRange.Max; p++ {
			availablePorts = append(availablePorts, p)
		}
	}

	portInUse := make(map[int]bool)
	assignedPorts := make(map[string][]int)
	assignedVolumes := make(map[string][]string)

	// Detect ports already in use by existing containers
	ctx := context.Background()
	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("failed to list existing containers: %w", err)
	}

	// Inspect each container to find which host ports are in use
	for _, c := range containers {
		containerJSON, err := cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect container %s: %w", c.ID, err)
		}

		// Track ports used by this container
		containerPorts := []int{}

		// Also check HostConfig.PortBindings (for all containers, including stopped ones)
		if containerJSON.HostConfig != nil && containerJSON.HostConfig.PortBindings != nil {
			for _, bindings := range containerJSON.HostConfig.PortBindings {
				for _, binding := range bindings {
					if binding.HostPort != "" {
						// Parse the host port
						var hostPort int
						_, err := fmt.Sscanf(binding.HostPort, "%d", &hostPort)
						if err == nil {
							portInUse[hostPort] = true
							containerPorts = append(containerPorts, hostPort)
						}
					}
				}
			}
		}

		// Store assigned ports for this container
		if len(containerPorts) > 0 {
			assignedPorts[c.ID] = containerPorts
		}

		// Track volumes used by this container
		if len(containerJSON.Mounts) > 0 {
			containerVolumes := []string{}
			for _, mount := range containerJSON.Mounts {
				if mount.Type == "bind" {
					containerVolumes = append(containerVolumes, mount.Source)
				}
			}
			if len(containerVolumes) > 0 {
				assignedVolumes[c.ID] = containerVolumes
			}
		}
	}

	return &dockerService{
		client:          cli,
		config:          config,
		assignedPorts:   assignedPorts,
		assignedVolumes: assignedVolumes,
		availablePorts:  availablePorts,
		portInUse:       portInUse,
	}, nil
}

// CreateContainer creates a new Docker container with the specified configuration
func (d *dockerService) CreateContainer(ctx context.Context, config ContainerConfig) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Pull image if not exists
	if err := d.pullImageIfNotExists(ctx, config.Image); err != nil {
		return "", fmt.Errorf("failed to ensure image: %w", err)
	}

	// Prepare port bindings
	portBindings, exposedPorts, assignedPorts, err := d.preparePorts(config.Ports)
	if err != nil {
		return "", fmt.Errorf("failed to prepare ports: %w", err)
	}

	// Prepare volumes
	mounts, volumePaths, err := d.prepareVolumes(config.Volumes)
	if err != nil {
		// Cleanup allocated ports on failure
		for _, port := range assignedPorts {
			d.portInUse[port] = false
		}
		return "", fmt.Errorf("failed to prepare volumes: %w", err)
	}

	// Prepare restart policy
	restartPolicy := d.getRestartPolicy(config.RestartPolicy)

	// Create container configuration
	containerConfig := &container.Config{
		Image:        config.Image,
		Cmd:          config.Cmd,
		Env:          config.Env,
		ExposedPorts: exposedPorts,
	}

	hostConfig := &container.HostConfig{
		PortBindings:  portBindings,
		Mounts:        mounts,
		RestartPolicy: restartPolicy,
	}

	// Set resource limits
	if config.CPULimit > 0 {
		hostConfig.Resources.NanoCPUs = config.CPULimit
	}
	if config.MemoryLimit > 0 {
		hostConfig.Resources.Memory = config.MemoryLimit
	}

	// Create the container
	resp, err := d.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, config.Name)
	if err != nil {
		// Cleanup allocated resources on failure
		for _, port := range assignedPorts {
			d.portInUse[port] = false
		}
		d.cleanupVolumes(volumePaths)
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	// Store assigned resources
	d.assignedPorts[resp.ID] = assignedPorts
	d.assignedVolumes[resp.ID] = volumePaths

	return resp.ID, nil
}

// Start starts a container
func (d *dockerService) Start(ctx context.Context, containerID string) error {
	if err := d.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}
	return nil
}

// Stop stops a container
func (d *dockerService) Stop(ctx context.Context, containerID string, timeout *int) error {
	var stopTimeout *int
	if timeout != nil {
		stopTimeout = timeout
	}

	if err := d.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: stopTimeout}); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}
	return nil
}

// Restart restarts a container
func (d *dockerService) Restart(ctx context.Context, containerID string, timeout *int) error {
	var restartTimeout *int
	if timeout != nil {
		restartTimeout = timeout
	}

	if err := d.client.ContainerRestart(ctx, containerID, container.StopOptions{Timeout: restartTimeout}); err != nil {
		return fmt.Errorf("failed to restart container: %w", err)
	}
	return nil
}

// Remove removes a container and releases its resources
func (d *dockerService) Remove(ctx context.Context, containerID string, force bool) error {
	if err := d.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: force}); err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	// Release resources
	d.releasePorts(containerID)
	if err := d.releaseVolumes(containerID); err != nil {
		return fmt.Errorf("failed to release volumes: %w", err)
	}

	return nil
}

// GetLogs retrieves container logs
func (d *dockerService) GetLogs(ctx context.Context, containerID string, tail string, follow bool) (io.ReadCloser, error) {
	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tail,
	}

	logs, err := d.client.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return nil, fmt.Errorf("failed to get container logs: %w", err)
	}

	return logs, nil
}

// Exec executes a command in a running container
func (d *dockerService) Exec(ctx context.Context, containerID string, cmd []string) (string, error) {
	execConfig := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	}

	execIDResp, err := d.client.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create exec instance: %w", err)
	}

	attachResp, err := d.client.ContainerExecAttach(ctx, execIDResp.ID, container.ExecStartOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to attach to exec instance: %w", err)
	}
	defer attachResp.Close()

	output, err := io.ReadAll(attachResp.Reader)
	if err != nil {
		return "", fmt.Errorf("failed to read exec output: %w", err)
	}

	return string(output), nil
}

// InspectContainer returns detailed container information
func (d *dockerService) InspectContainer(ctx context.Context, containerID string) (*types.ContainerJSON, error) {
	containerJSON, err := d.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}
	return &containerJSON, nil
}

// ListContainers lists all containers with management tracking information
func (d *dockerService) ListContainers(ctx context.Context, runningOnly bool) ([]ContainerInfo, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	// List containers from Docker
	options := container.ListOptions{
		All: !runningOnly,
	}

	containers, err := d.client.ContainerList(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	// Build ContainerInfo slice
	result := make([]ContainerInfo, 0, len(containers))

	for _, c := range containers {
		info := ContainerInfo{
			ID:    c.ID,
			State: c.State,
			Image: c.Image,
		}

		// Get container name (remove leading slash if present)
		if len(c.Names) > 0 {
			info.Name = c.Names[0]
			if len(info.Name) > 0 && info.Name[0] == '/' {
				info.Name = info.Name[1:]
			}
		}

		// Parse port mappings
		info.Ports = make([]PortMapping, 0, len(c.Ports))
		for _, port := range c.Ports {
			if port.PublicPort > 0 {
				info.Ports = append(info.Ports, PortMapping{
					ContainerPort: int(port.PrivatePort),
					HostPort:      int(port.PublicPort),
					Protocol:      port.Type,
				})
			}
		}

		// Check if container is managed by this service
		if assignedPorts, exists := d.assignedPorts[c.ID]; exists {
			info.IsManaged = true
			info.AssignedPorts = make([]int, len(assignedPorts))
			copy(info.AssignedPorts, assignedPorts)
		}

		if assignedVolumes, exists := d.assignedVolumes[c.ID]; exists {
			info.IsManaged = true
			info.AssignedVolumes = make([]string, len(assignedVolumes))
			copy(info.AssignedVolumes, assignedVolumes)
		}

		result = append(result, info)
	}

	return result, nil
}

// GetAssignedPorts returns the list of ports assigned to a container
func (d *dockerService) GetAssignedPorts(containerID string) []int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	ports, exists := d.assignedPorts[containerID]
	if !exists {
		return []int{}
	}

	// Return a copy to prevent external modification
	result := make([]int, len(ports))
	copy(result, ports)
	return result
}

// GetAssignedVolumes returns the list of volume paths assigned to a container
func (d *dockerService) GetAssignedVolumes(containerID string) []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	volumes, exists := d.assignedVolumes[containerID]
	if !exists {
		return []string{}
	}

	// Return a copy to prevent external modification
	result := make([]string, len(volumes))
	copy(result, volumes)
	return result
}

// releasePorts releases ports assigned to a container back to the pool
func (d *dockerService) releasePorts(containerID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if ports, exists := d.assignedPorts[containerID]; exists {
		for _, port := range ports {
			d.portInUse[port] = false
		}
		delete(d.assignedPorts, containerID)
	}
}

// releaseVolumes removes volumes assigned to a container
func (d *dockerService) releaseVolumes(containerID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if volumes, exists := d.assignedVolumes[containerID]; exists {
		if err := d.cleanupVolumes(volumes); err != nil {
			return err
		}
		delete(d.assignedVolumes, containerID)
	}
	return nil
}

// Close closes the Docker client connection
func (d *dockerService) Close() error {
	if d.client != nil {
		return d.client.Close()
	}
	return nil
}

// Helper methods

func (d *dockerService) pullImageIfNotExists(ctx context.Context, img string) error {
	// Check if image exists locally
	filter := filters.NewArgs()
	filter.Add("reference", img)

	images, err := d.client.ImageList(ctx, image.ListOptions{Filters: filter})
	if err != nil {
		return fmt.Errorf("failed to list images: %w", err)
	}

	if len(images) > 0 {
		return nil // Image already exists
	}

	// Pull the image
	reader, err := d.client.ImagePull(ctx, img, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer reader.Close()

	// Consume the output to ensure pull completes
	_, err = io.Copy(io.Discard, reader)
	if err != nil {
		return fmt.Errorf("failed to read image pull output: %w", err)
	}

	return nil
}

func (d *dockerService) preparePorts(portConfigs []PortConfig) (nat.PortMap, nat.PortSet, []int, error) {
	portBindings := nat.PortMap{}
	exposedPorts := nat.PortSet{}
	assignedPorts := []int{}

	for _, pc := range portConfigs {
		protocol := pc.Protocol
		if protocol == "" {
			protocol = "tcp"
		}

		containerPort := nat.Port(fmt.Sprintf("%d/%s", pc.ContainerPort, protocol))
		exposedPorts[containerPort] = struct{}{}

		hostPort := pc.HostPort
		if hostPort == 0 {
			// Auto-assign port
			port, err := d.allocatePort()
			if err != nil {
				// Cleanup already allocated ports
				for _, p := range assignedPorts {
					d.portInUse[p] = false
				}
				return nil, nil, nil, err
			}
			hostPort = port
			assignedPorts = append(assignedPorts, port)
		}

		portBindings[containerPort] = []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: fmt.Sprintf("%d", hostPort),
			},
		}
	}

	return portBindings, exposedPorts, assignedPorts, nil
}

func (d *dockerService) allocatePort() (int, error) {
	for _, port := range d.availablePorts {
		if !d.portInUse[port] {
			d.portInUse[port] = true
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available ports in pool")
}

func (d *dockerService) prepareVolumes(volumeConfigs []VolumeConfig) ([]mount.Mount, []string, error) {
	mounts := []mount.Mount{}
	volumePaths := []string{}

	for _, vc := range volumeConfigs {
		hostPath := vc.HostPath

		if hostPath == "" {
			// Auto-generate host path
			hostPath = filepath.Join(d.config.BaseVolumePath, vc.Name)

			// Create directory
			if err := os.MkdirAll(hostPath, 0755); err != nil {
				// Cleanup already created volumes
				d.cleanupVolumes(volumePaths)
				return nil, nil, fmt.Errorf("failed to create volume directory %s: %w", hostPath, err)
			}

			volumePaths = append(volumePaths, hostPath)
		}

		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   hostPath,
			Target:   vc.Target,
			ReadOnly: vc.ReadOnly,
		})
	}

	return mounts, volumePaths, nil
}

func (d *dockerService) cleanupVolumes(volumePaths []string) error {
	for _, path := range volumePaths {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("failed to remove volume directory %s: %w", path, err)
		}
	}
	return nil
}

func (d *dockerService) getRestartPolicy(policy string) container.RestartPolicy {
	switch policy {
	case "always":
		return container.RestartPolicy{Name: container.RestartPolicyAlways}
	case "on-failure":
		return container.RestartPolicy{Name: container.RestartPolicyOnFailure}
	case "unless-stopped":
		return container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
	default:
		return container.RestartPolicy{Name: container.RestartPolicyDisabled}
	}
}
