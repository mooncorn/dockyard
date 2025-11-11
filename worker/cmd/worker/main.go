package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mooncorn/dockyard/worker/internal/client"
	"github.com/mooncorn/dockyard/worker/internal/service"
)

// validateVolumeBasePath checks if the worker has write permissions to the volume base path
func validateVolumeBasePath(path string) error {
	// Try to create the base directory
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf(`volume base path validation failed: cannot create directory at "%s": %w

This typically happens when:
  - The path requires root/elevated permissions (e.g., /var/dockyard)
  - The worker process doesn't have write access to the parent directory

Solutions:
  1. Run the worker as root or with sudo:
     sudo ./worker --server=... --token=...

  2. Use a user-writable path (recommended for development):
     ./worker --server=... --token=... --volume-base-path=$HOME/dockyard-volumes

  3. Pre-create the directory with proper permissions:
     sudo mkdir -p %s
     sudo chown -R $USER:$(id -gn) %s
     ./worker --server=... --token=...`, path, path, filepath.Dir(path))
	}

	// Try to create a test file to verify write access
	testFile := filepath.Join(path, ".write-test")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		return fmt.Errorf(`volume base path validation failed: cannot write to directory at "%s": %w

The directory exists but the worker cannot write files to it.

Solutions:
  1. Fix directory permissions:
     sudo chown -R $USER:$(id -gn) %s
     sudo chmod -R 775 %s

  2. Use a different path:
     ./worker --server=... --token=... --volume-base-path=$HOME/dockyard-volumes`, path, path, path)
	}

	// Clean up test file
	os.Remove(testFile)

	return nil
}

func main() {
	// Command line flags
	var (
		serverURL         = flag.String("server", "localhost:8080", "gRPC server URL to connect to")
		token             = flag.String("token", "worker-token-123", "Authentication token")
		useTLS            = flag.Bool("tls", false, "Use TLS connection")
		reconnect         = flag.Bool("reconnect", true, "Enable automatic reconnection")
		maxConnectRetries = flag.Int("max-connect-retries", 0, "Maximum connection retry attempts (0 = infinite)")
		volumeBasePath    = flag.String("volume-base-path", "/var/dockyard/volumes", "Base path for container volumes")
		portRangeMin      = flag.Int("port-range-min", 10000, "Minimum port in available range")
		portRangeMax      = flag.Int("port-range-max", 20000, "Maximum port in available range")
		cpuBudget         = flag.Float64("cpu-budget", 0, "CPU cores budget (0 = auto-detect)")
		memoryBudget      = flag.Int64("memory-budget", 0, "Memory budget in MB (0 = auto-detect)")
		autoReserve       = flag.Bool("auto-reserve", true, "Auto-reserve 80% of system resources")
	)
	flag.Parse()

	log.Printf("Starting Dockyard Worker...")

	// Validate volume base path before proceeding
	log.Printf("Validating volume base path: %s", *volumeBasePath)
	if err := validateVolumeBasePath(*volumeBasePath); err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("✅ Volume base path validated")

	// Create resource budget
	budget, err := service.NewResourceBudget(*cpuBudget, *memoryBudget, *autoReserve)
	if err != nil {
		log.Fatalf("Failed to create resource budget: %v", err)
	}

	log.Printf("✅ Resource budget initialized")
	log.Printf("   CPU cores: %.2f", budget.GetAvailableCPU())
	log.Printf("   Memory: %d MB", budget.GetAvailableMemory())
	log.Printf("   Auto-reserve: %v", budget.IsAutoReserve)

	// Create Docker service
	dockerConfig := service.DockerServiceConfig{
		BaseVolumePath: *volumeBasePath,
		PortRange: &service.PortRange{
			Min: *portRangeMin,
			Max: *portRangeMax,
		},
	}

	dockerService, err := service.NewDockerService(dockerConfig, budget)
	if err != nil {
		log.Fatalf("Failed to create Docker service: %v", err)
	}
	defer dockerService.Close()

	log.Printf("✅ Docker service initialized")
	log.Printf("   Volume base path: %s", *volumeBasePath)
	log.Printf("   Port range: %d-%d", *portRangeMin, *portRangeMax)

	ctx := context.Background()

	containers, err := dockerService.ListContainers(ctx, false)
	if err != nil {
		log.Fatalf("Failed to list containers: %v", err)
	}

	fmt.Printf("Containers: %v", containers)

	// Create gRPC client configuration
	config := client.GRPCClientConfig{
		ServerURL:         *serverURL,
		Token:             *token,
		UseTLS:            *useTLS,
		Reconnect:         *reconnect,
		ReconnectDelay:    5 * time.Second,
		MaxConnectRetries: *maxConnectRetries,
		DockerService:     dockerService,
		Budget:            budget,
	}

	// Create and connect client
	grpcClient := client.NewGRPCClient(config)

	log.Printf("Connecting to server at %s...", *serverURL)
	err = grpcClient.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to server: %v", err)
	}

	log.Printf("✅ Connected to gRPC server")
	log.Printf("🔄 Listening for ping messages...")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Printf("Received shutdown signal, disconnecting...")

	// Graceful shutdown
	grpcClient.Disconnect()
	log.Printf("Worker stopped")
}
