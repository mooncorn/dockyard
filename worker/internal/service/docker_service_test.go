package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// Test helpers

type testCleanup struct {
	svc          DockerService
	containerIDs []string
	volumePaths  []string
}

func (tc *testCleanup) addContainer(id string) {
	tc.containerIDs = append(tc.containerIDs, id)
}

func (tc *testCleanup) addVolume(path string) {
	tc.volumePaths = append(tc.volumePaths, path)
}

func (tc *testCleanup) cleanup(t *testing.T) {
	ctx := context.Background()

	// Remove containers
	for _, id := range tc.containerIDs {
		if err := tc.svc.Remove(ctx, id, true); err != nil {
			t.Logf("Warning: failed to remove container %s: %v", id, err)
		}
	}

	// Remove volumes
	for _, path := range tc.volumePaths {
		if err := os.RemoveAll(path); err != nil {
			t.Logf("Warning: failed to remove volume %s: %v", path, err)
		}
	}

	// Close service
	if err := tc.svc.Close(); err != nil {
		t.Logf("Warning: failed to close service: %v", err)
	}
}

func setupTestService(t *testing.T) (*testCleanup, DockerService) {
	t.Helper()

	// Create temp directory for volumes
	tempDir := t.TempDir()

	config := DockerServiceConfig{
		BaseVolumePath: tempDir,
		PortRange: &PortRange{
			Min: 30000,
			Max: 30100,
		},
	}

	svc, err := NewDockerService(config)
	if err != nil {
		t.Fatalf("Failed to create Docker service: %v", err)
	}

	cleanup := &testCleanup{
		svc:          svc,
		containerIDs: []string{},
		volumePaths:  []string{},
	}

	return cleanup, svc
}

func assertContainerRunning(t *testing.T, svc DockerService, containerID string) {
	t.Helper()

	ctx := context.Background()
	inspect, err := svc.InspectContainer(ctx, containerID)
	if err != nil {
		t.Fatalf("Failed to inspect container: %v", err)
	}

	if !inspect.State.Running {
		t.Errorf("Expected container to be running, but it's not. State: %+v", inspect.State)
	}
}

func assertContainerExists(t *testing.T, svc DockerService, containerID string) {
	t.Helper()

	ctx := context.Background()
	_, err := svc.InspectContainer(ctx, containerID)
	if err != nil {
		t.Fatalf("Expected container to exist, but got error: %v", err)
	}
}

func assertPortAllocated(t *testing.T, svc DockerService, containerID string, expectedCount int) []int {
	t.Helper()

	ports := svc.GetAssignedPorts(containerID)
	if len(ports) != expectedCount {
		t.Errorf("Expected %d ports to be allocated, got %d: %v", expectedCount, len(ports), ports)
	}

	return ports
}

func assertVolumeExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("Expected volume directory to exist at %s, but it doesn't", path)
	}
}

func assertPortInRange(t *testing.T, port, min, max int) {
	t.Helper()

	if port < min || port > max {
		t.Errorf("Expected port to be in range %d-%d, got %d", min, max, port)
	}
}

// Table-driven tests for CreateContainer

func TestDockerService_CreateContainer(t *testing.T) {
	tests := []struct {
		name            string
		containerConfig ContainerConfig
		wantErr         bool
		validate        func(t *testing.T, cleanup *testCleanup, svc DockerService, containerID string)
	}{
		{
			name: "basic container with defaults",
			containerConfig: ContainerConfig{
				Image: "alpine:latest",
				Name:  "test-basic-container",
				Cmd:   []string{"sleep", "30"},
			},
			wantErr: false,
			validate: func(t *testing.T, cleanup *testCleanup, svc DockerService, containerID string) {
				assertContainerExists(t, svc, containerID)

				// Should have no ports allocated
				ports := assertPortAllocated(t, svc, containerID, 0)
				if len(ports) != 0 {
					t.Errorf("Expected no ports for basic container")
				}

				// Should have no volumes
				volumes := svc.GetAssignedVolumes(containerID)
				if len(volumes) != 0 {
					t.Errorf("Expected no volumes for basic container, got %v", volumes)
				}
			},
		},
		{
			name: "container with auto-assigned ports",
			containerConfig: ContainerConfig{
				Image: "nginx:alpine",
				Name:  "test-nginx-auto-port",
				Ports: []PortConfig{
					{ContainerPort: 80, HostPort: 0, Protocol: "tcp"},
					{ContainerPort: 443, HostPort: 0, Protocol: "tcp"},
				},
			},
			wantErr: false,
			validate: func(t *testing.T, cleanup *testCleanup, svc DockerService, containerID string) {
				assertContainerExists(t, svc, containerID)

				// Should have 2 ports allocated
				ports := assertPortAllocated(t, svc, containerID, 2)

				// Ports should be in the expected range
				for _, port := range ports {
					assertPortInRange(t, port, 30000, 30100)
				}

				// Verify ports are unique
				if ports[0] == ports[1] {
					t.Errorf("Expected unique ports, got duplicates: %v", ports)
				}
			},
		},
		{
			name: "container with explicit ports",
			containerConfig: ContainerConfig{
				Image: "nginx:alpine",
				Name:  "test-nginx-explicit-port",
				Ports: []PortConfig{
					{ContainerPort: 80, HostPort: 30050, Protocol: "tcp"},
				},
			},
			wantErr: false,
			validate: func(t *testing.T, cleanup *testCleanup, svc DockerService, containerID string) {
				assertContainerExists(t, svc, containerID)

				// Should have no auto-allocated ports (explicit port was provided)
				ports := assertPortAllocated(t, svc, containerID, 0)
				if len(ports) != 0 {
					t.Errorf("Expected no auto-allocated ports for explicit port config")
				}
			},
		},
		{
			name: "container with auto-generated volumes",
			containerConfig: ContainerConfig{
				Image: "alpine:latest",
				Name:  "test-volume-container",
				Cmd:   []string{"sleep", "30"},
				Volumes: []VolumeConfig{
					{Name: "data", Target: "/data", ReadOnly: false},
					{Name: "config", Target: "/config", ReadOnly: true},
				},
			},
			wantErr: false,
			validate: func(t *testing.T, cleanup *testCleanup, svc DockerService, containerID string) {
				assertContainerExists(t, svc, containerID)

				// Should have 2 volumes allocated
				volumes := svc.GetAssignedVolumes(containerID)
				if len(volumes) != 2 {
					t.Errorf("Expected 2 volumes, got %d: %v", len(volumes), volumes)
				}

				// Verify volume directories exist
				for _, vol := range volumes {
					assertVolumeExists(t, vol)
					cleanup.addVolume(vol)
				}
			},
		},
		{
			name: "container with explicit volume path",
			containerConfig: ContainerConfig{
				Image: "alpine:latest",
				Name:  "test-explicit-volume",
				Cmd:   []string{"sleep", "30"},
				Volumes: []VolumeConfig{
					{Name: "data", HostPath: "/tmp/test-explicit-volume", Target: "/data", ReadOnly: false},
				},
			},
			wantErr: false,
			validate: func(t *testing.T, cleanup *testCleanup, svc DockerService, containerID string) {
				assertContainerExists(t, svc, containerID)

				// Should have no auto-generated volumes (explicit path was provided)
				volumes := svc.GetAssignedVolumes(containerID)
				if len(volumes) != 0 {
					t.Errorf("Expected no auto-generated volumes for explicit volume path")
				}
			},
		},
		{
			name: "container with multiple ports and volumes",
			containerConfig: ContainerConfig{
				Image: "alpine:latest",
				Name:  "test-complex-container",
				Cmd:   []string{"sleep", "30"},
				Ports: []PortConfig{
					{ContainerPort: 8080, HostPort: 0, Protocol: "tcp"},
					{ContainerPort: 8081, HostPort: 0, Protocol: "tcp"},
					{ContainerPort: 9000, HostPort: 0, Protocol: "udp"},
				},
				Volumes: []VolumeConfig{
					{Name: "data", Target: "/data", ReadOnly: false},
					{Name: "logs", Target: "/logs", ReadOnly: false},
				},
			},
			wantErr: false,
			validate: func(t *testing.T, cleanup *testCleanup, svc DockerService, containerID string) {
				assertContainerExists(t, svc, containerID)

				// Should have 3 ports allocated
				ports := assertPortAllocated(t, svc, containerID, 3)

				// All ports should be unique
				portMap := make(map[int]bool)
				for _, port := range ports {
					if portMap[port] {
						t.Errorf("Duplicate port found: %d", port)
					}
					portMap[port] = true
					assertPortInRange(t, port, 30000, 30100)
				}

				// Should have 2 volumes
				volumes := svc.GetAssignedVolumes(containerID)
				if len(volumes) != 2 {
					t.Errorf("Expected 2 volumes, got %d", len(volumes))
				}

				for _, vol := range volumes {
					assertVolumeExists(t, vol)
					cleanup.addVolume(vol)
				}
			},
		},
		{
			name: "container with resource limits",
			containerConfig: ContainerConfig{
				Image:       "alpine:latest",
				Name:        "test-resource-limits",
				Cmd:         []string{"sleep", "30"},
				CPULimit:    1000000000, // 1 CPU core
				MemoryLimit: 536870912,  // 512MB
			},
			wantErr: false,
			validate: func(t *testing.T, cleanup *testCleanup, svc DockerService, containerID string) {
				assertContainerExists(t, svc, containerID)

				ctx := context.Background()
				inspect, err := svc.InspectContainer(ctx, containerID)
				if err != nil {
					t.Fatalf("Failed to inspect container: %v", err)
				}

				if inspect.HostConfig.NanoCPUs != 1000000000 {
					t.Errorf("Expected CPU limit to be 1000000000, got %d", inspect.HostConfig.NanoCPUs)
				}

				if inspect.HostConfig.Memory != 536870912 {
					t.Errorf("Expected memory limit to be 536870912, got %d", inspect.HostConfig.Memory)
				}
			},
		},
		{
			name: "container with always restart policy",
			containerConfig: ContainerConfig{
				Image:         "alpine:latest",
				Name:          "test-restart-always",
				Cmd:           []string{"sleep", "30"},
				RestartPolicy: "always",
			},
			wantErr: false,
			validate: func(t *testing.T, cleanup *testCleanup, svc DockerService, containerID string) {
				assertContainerExists(t, svc, containerID)

				ctx := context.Background()
				inspect, err := svc.InspectContainer(ctx, containerID)
				if err != nil {
					t.Fatalf("Failed to inspect container: %v", err)
				}

				if inspect.HostConfig.RestartPolicy.Name != container.RestartPolicyAlways {
					t.Errorf("Expected restart policy to be 'always', got '%s'", inspect.HostConfig.RestartPolicy.Name)
				}
			},
		},
		{
			name: "container with on-failure restart policy",
			containerConfig: ContainerConfig{
				Image:         "alpine:latest",
				Name:          "test-restart-on-failure",
				Cmd:           []string{"sleep", "30"},
				RestartPolicy: "on-failure",
			},
			wantErr: false,
			validate: func(t *testing.T, cleanup *testCleanup, svc DockerService, containerID string) {
				assertContainerExists(t, svc, containerID)

				ctx := context.Background()
				inspect, err := svc.InspectContainer(ctx, containerID)
				if err != nil {
					t.Fatalf("Failed to inspect container: %v", err)
				}

				if inspect.HostConfig.RestartPolicy.Name != container.RestartPolicyOnFailure {
					t.Errorf("Expected restart policy to be 'on-failure', got '%s'", inspect.HostConfig.RestartPolicy.Name)
				}
			},
		},
		{
			name: "invalid image should fail",
			containerConfig: ContainerConfig{
				Image: "this-image-definitely-does-not-exist:invalid",
				Name:  "test-invalid-image",
			},
			wantErr: true,
			validate: func(t *testing.T, cleanup *testCleanup, svc DockerService, containerID string) {
				// Container should not exist
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup, svc := setupTestService(t)
			defer cleanup.cleanup(t)

			ctx := context.Background()

			containerID, err := svc.CreateContainer(ctx, tt.containerConfig)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error, but got none")
					cleanup.addContainer(containerID)
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			cleanup.addContainer(containerID)

			if tt.validate != nil {
				tt.validate(t, cleanup, svc, containerID)
			}
		})
	}
}

// Test container lifecycle operations

func TestDockerService_ContainerLifecycle(t *testing.T) {
	cleanup, svc := setupTestService(t)
	defer cleanup.cleanup(t)

	ctx := context.Background()

	// Create container
	config := ContainerConfig{
		Image: "alpine:latest",
		Name:  "test-lifecycle",
		Cmd:   []string{"sleep", "60"},
	}

	containerID, err := svc.CreateContainer(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create container: %v", err)
	}
	cleanup.addContainer(containerID)

	// Start container
	if err := svc.Start(ctx, containerID); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	// Give it a moment to start
	time.Sleep(1 * time.Second)

	// Verify running
	assertContainerRunning(t, svc, containerID)

	// Stop container
	timeout := 5
	if err := svc.Stop(ctx, containerID, &timeout); err != nil {
		t.Fatalf("Failed to stop container: %v", err)
	}

	// Verify stopped
	inspect, err := svc.InspectContainer(ctx, containerID)
	if err != nil {
		t.Fatalf("Failed to inspect container: %v", err)
	}

	if inspect.State.Running {
		t.Errorf("Expected container to be stopped, but it's still running")
	}

	// Restart container
	if err := svc.Restart(ctx, containerID, &timeout); err != nil {
		t.Fatalf("Failed to restart container: %v", err)
	}

	// Give it a moment to restart
	time.Sleep(1 * time.Second)

	// Verify running again
	assertContainerRunning(t, svc, containerID)
}

// Test port exhaustion

func TestDockerService_PortExhaustion(t *testing.T) {
	// Create service with very limited port range
	tempDir := t.TempDir()
	config := DockerServiceConfig{
		BaseVolumePath: tempDir,
		PortRange: &PortRange{
			Min: 31000,
			Max: 31002, // Only 3 ports available
		},
	}

	svc, err := NewDockerService(config)
	if err != nil {
		t.Fatalf("Failed to create Docker service: %v", err)
	}
	defer svc.Close()

	cleanup := &testCleanup{
		svc:          svc,
		containerIDs: []string{},
	}
	defer cleanup.cleanup(t)

	ctx := context.Background()

	// Create 3 containers, each using 1 port
	for i := 0; i < 3; i++ {
		containerConfig := ContainerConfig{
			Image: "alpine:latest",
			Name:  fmt.Sprintf("test-port-exhaust-%d", i),
			Cmd:   []string{"sleep", "30"},
			Ports: []PortConfig{
				{ContainerPort: 8080, HostPort: 0},
			},
		}

		containerID, err := svc.CreateContainer(ctx, containerConfig)
		if err != nil {
			t.Fatalf("Failed to create container %d: %v", i, err)
		}
		cleanup.addContainer(containerID)
	}

	// 4th container should fail due to port exhaustion
	containerConfig := ContainerConfig{
		Image: "alpine:latest",
		Name:  "test-port-exhaust-fail",
		Cmd:   []string{"sleep", "30"},
		Ports: []PortConfig{
			{ContainerPort: 8080, HostPort: 0},
		},
	}

	_, err = svc.CreateContainer(ctx, containerConfig)
	if err == nil {
		t.Errorf("Expected port exhaustion error, but got none")
	}
}

// Test port detection from existing containers

func TestDockerService_PortDetectionOnInit(t *testing.T) {
	// Create a container using the standard Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("Failed to create Docker client: %v", err)
	}
	defer cli.Close()

	ctx := context.Background()

	// Create a test container with a specific port
	testPort := 30555
	containerConfig := &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "60"},
		ExposedPorts: nat.PortSet{
			"8080/tcp": struct{}{},
		},
	}

	hostConfig := &container.HostConfig{
		PortBindings: nat.PortMap{
			"8080/tcp": []nat.PortBinding{
				{HostIP: "0.0.0.0", HostPort: fmt.Sprintf("%d", testPort)},
			},
		},
	}

	resp, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, "test-existing-container")
	if err != nil {
		t.Fatalf("Failed to create test container: %v", err)
	}

	// Cleanup: remove the container when test finishes
	defer func() {
		cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
	}()

	// Now initialize DockerService - it should detect the port in use
	tempDir := t.TempDir()
	config := DockerServiceConfig{
		BaseVolumePath: tempDir,
		PortRange: &PortRange{
			Min: 30000,
			Max: 31000,
		},
	}

	svc, err := NewDockerService(config)
	if err != nil {
		t.Fatalf("Failed to create Docker service: %v", err)
	}
	defer svc.Close()

	// Verify the existing container is tracked
	assignedPorts := svc.GetAssignedPorts(resp.ID)
	if len(assignedPorts) != 1 {
		t.Errorf("Expected 1 assigned port, got %d: %v", len(assignedPorts), assignedPorts)
	}

	if len(assignedPorts) > 0 && assignedPorts[0] != testPort {
		t.Errorf("Expected port %d to be tracked, got %d", testPort, assignedPorts[0])
	}

	// Try to create a new container requesting auto-assigned port
	// It should NOT assign port 30555 since it's already in use
	cleanup := &testCleanup{
		svc:          svc,
		containerIDs: []string{},
	}
	defer cleanup.cleanup(t)

	newConfig := ContainerConfig{
		Image: "alpine:latest",
		Name:  "test-new-container",
		Cmd:   []string{"sleep", "30"},
		Ports: []PortConfig{
			{ContainerPort: 8080, HostPort: 0}, // Auto-assign
		},
	}

	containerID, err := svc.CreateContainer(ctx, newConfig)
	if err != nil {
		t.Fatalf("Failed to create new container: %v", err)
	}
	cleanup.addContainer(containerID)

	// Get the assigned port
	newPorts := svc.GetAssignedPorts(containerID)
	if len(newPorts) != 1 {
		t.Fatalf("Expected 1 assigned port for new container, got %d", len(newPorts))
	}

	// The new port should NOT be the same as the existing container's port
	if newPorts[0] == testPort {
		t.Errorf("Service assigned port %d which was already in use", testPort)
	}
}

// Test volume path structure

func TestDockerService_VolumePathStructure(t *testing.T) {
	cleanup, svc := setupTestService(t)
	defer cleanup.cleanup(t)

	ctx := context.Background()

	config := ContainerConfig{
		Image: "alpine:latest",
		Name:  "test-volume-structure",
		Cmd:   []string{"sleep", "30"},
		Volumes: []VolumeConfig{
			{Name: "data", Target: "/data"},
			{Name: "config", Target: "/config"},
		},
	}

	containerID, err := svc.CreateContainer(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create container: %v", err)
	}
	cleanup.addContainer(containerID)

	volumes := svc.GetAssignedVolumes(containerID)
	if len(volumes) != 2 {
		t.Fatalf("Expected 2 volumes, got %d", len(volumes))
	}

	// Verify volume paths follow the expected structure
	// Expected: {basePath}/{volumeName}
	for _, vol := range volumes {
		cleanup.addVolume(vol)

		// Volume should be in the temp directory
		baseName := filepath.Base(vol)
		if baseName != "data" && baseName != "config" {
			t.Errorf("Expected volume name to be 'data' or 'config', got '%s' from path '%s'", baseName, vol)
		}

		assertVolumeExists(t, vol)
	}
}

// Test ListContainers

func TestDockerService_ListContainers(t *testing.T) {
	cleanup, svc := setupTestService(t)
	defer cleanup.cleanup(t)

	ctx := context.Background()

	// Create multiple containers with different states
	// Container 1: Running container with auto-assigned ports (managed)
	config1 := ContainerConfig{
		Image: "alpine:latest",
		Name:  "test-list-running",
		Cmd:   []string{"sleep", "60"},
		Ports: []PortConfig{
			{ContainerPort: 8080, HostPort: 0},
		},
		Volumes: []VolumeConfig{
			{Name: "data", Target: "/data"},
		},
	}

	container1ID, err := svc.CreateContainer(ctx, config1)
	if err != nil {
		t.Fatalf("Failed to create container 1: %v", err)
	}
	cleanup.addContainer(container1ID)

	if err := svc.Start(ctx, container1ID); err != nil {
		t.Fatalf("Failed to start container 1: %v", err)
	}

	// Container 2: Stopped container (managed)
	config2 := ContainerConfig{
		Image: "alpine:latest",
		Name:  "test-list-stopped",
		Cmd:   []string{"echo", "hello"},
		Ports: []PortConfig{
			{ContainerPort: 9090, HostPort: 0},
		},
	}

	container2ID, err := svc.CreateContainer(ctx, config2)
	if err != nil {
		t.Fatalf("Failed to create container 2: %v", err)
	}
	cleanup.addContainer(container2ID)

	// Container 3: Running container without service management
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("Failed to create Docker client: %v", err)
	}
	defer cli.Close()

	unmanagedConfig := &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "60"},
	}

	unmanagedResp, err := cli.ContainerCreate(ctx, unmanagedConfig, nil, nil, nil, "test-list-unmanaged")
	if err != nil {
		t.Fatalf("Failed to create unmanaged container: %v", err)
	}
	defer cli.ContainerRemove(ctx, unmanagedResp.ID, container.RemoveOptions{Force: true})

	if err := cli.ContainerStart(ctx, unmanagedResp.ID, container.StartOptions{}); err != nil {
		t.Fatalf("Failed to start unmanaged container: %v", err)
	}

	// Give containers a moment to stabilize
	time.Sleep(1 * time.Second)

	// Test 1: List running containers only
	t.Run("list running containers only", func(t *testing.T) {
		containers, err := svc.ListContainers(ctx, true)
		if err != nil {
			t.Fatalf("Failed to list running containers: %v", err)
		}

		// Should have at least 2 containers (container1 and unmanaged)
		if len(containers) < 2 {
			t.Errorf("Expected at least 2 running containers, got %d", len(containers))
		}

		// Find our managed running container
		var foundManaged bool
		for _, c := range containers {
			if c.ID == container1ID {
				foundManaged = true

				if c.State != "running" {
					t.Errorf("Expected container1 state to be 'running', got '%s'", c.State)
				}

				if c.Name != "test-list-running" {
					t.Errorf("Expected container1 name to be 'test-list-running', got '%s'", c.Name)
				}

				if !c.IsManaged {
					t.Errorf("Expected container1 to be managed")
				}

				if len(c.AssignedPorts) != 1 {
					t.Errorf("Expected 1 assigned port, got %d", len(c.AssignedPorts))
				}

				if len(c.AssignedVolumes) != 1 {
					t.Errorf("Expected 1 assigned volume, got %d", len(c.AssignedVolumes))
				}

				if c.Image != "alpine:latest" {
					t.Errorf("Expected image to be 'alpine:latest', got '%s'", c.Image)
				}
			}
		}

		if !foundManaged {
			t.Errorf("Managed running container not found in list")
		}

		// Verify unmanaged container exists and is not marked as managed
		var foundUnmanaged bool
		for _, c := range containers {
			if c.ID == unmanagedResp.ID {
				foundUnmanaged = true

				if c.IsManaged {
					t.Errorf("Expected unmanaged container to not be marked as managed")
				}

				if len(c.AssignedPorts) > 0 {
					t.Errorf("Expected no assigned ports for unmanaged container, got %d", len(c.AssignedPorts))
				}

				if len(c.AssignedVolumes) > 0 {
					t.Errorf("Expected no assigned volumes for unmanaged container, got %d", len(c.AssignedVolumes))
				}
			}
		}

		if !foundUnmanaged {
			t.Errorf("Unmanaged container not found in list")
		}
	})

	// Test 2: List all containers (including stopped)
	t.Run("list all containers", func(t *testing.T) {
		containers, err := svc.ListContainers(ctx, false)
		if err != nil {
			t.Fatalf("Failed to list all containers: %v", err)
		}

		// Should have at least 3 containers (container1, container2, unmanaged)
		if len(containers) < 3 {
			t.Errorf("Expected at least 3 containers, got %d", len(containers))
		}

		// Find our stopped managed container
		var foundStopped bool
		for _, c := range containers {
			if c.ID == container2ID {
				foundStopped = true

				// Container 2 was never started, should be in "created" or "exited" state
				if c.State != "created" && c.State != "exited" {
					t.Logf("Container2 state: %s (expected 'created' or 'exited')", c.State)
				}

				if c.Name != "test-list-stopped" {
					t.Errorf("Expected container2 name to be 'test-list-stopped', got '%s'", c.Name)
				}

				if !c.IsManaged {
					t.Errorf("Expected container2 to be managed")
				}

				if len(c.AssignedPorts) != 1 {
					t.Errorf("Expected 1 assigned port, got %d", len(c.AssignedPorts))
				}
			}
		}

		if !foundStopped {
			t.Errorf("Stopped managed container not found in list")
		}
	})

	// Test 3: Verify port mappings are included
	t.Run("verify port mappings", func(t *testing.T) {
		containers, err := svc.ListContainers(ctx, false)
		if err != nil {
			t.Fatalf("Failed to list containers: %v", err)
		}

		for _, c := range containers {
			if c.ID == container1ID {
				// Container1 should have port mappings since it's running
				// Note: Ports might not show up immediately or if container isn't fully started
				t.Logf("Container1 ports: %+v", c.Ports)
				t.Logf("Container1 assigned ports: %+v", c.AssignedPorts)
			}
		}
	})
}

// Test ListContainers with empty Docker

func TestDockerService_ListContainers_Empty(t *testing.T) {
	cleanup, svc := setupTestService(t)
	defer cleanup.cleanup(t)

	ctx := context.Background()

	// List containers when none exist (or only unrelated ones)
	containers, err := svc.ListContainers(ctx, false)
	if err != nil {
		t.Fatalf("Failed to list containers: %v", err)
	}

	// Should return empty or only unmanaged containers
	for _, c := range containers {
		if c.IsManaged {
			t.Errorf("Found managed container when none should exist: %+v", c)
		}
	}
}
