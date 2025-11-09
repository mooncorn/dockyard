package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mooncorn/dockyard/worker/internal/client"
	"github.com/mooncorn/dockyard/worker/internal/service"
)

func main() {
	// Command line flags
	var (
		serverURL      = flag.String("server", "localhost:8080", "gRPC server URL to connect to")
		token          = flag.String("token", "worker-token-123", "Authentication token")
		useTLS         = flag.Bool("tls", false, "Use TLS connection")
		reconnect      = flag.Bool("reconnect", true, "Enable automatic reconnection")
		volumeBasePath = flag.String("volume-base-path", "/var/dockyard/volumes", "Base path for container volumes")
		portRangeMin   = flag.Int("port-range-min", 10000, "Minimum port in available range")
		portRangeMax   = flag.Int("port-range-max", 20000, "Maximum port in available range")
		cpuBudget      = flag.Float64("cpu-budget", 0, "CPU cores budget (0 = auto-detect)")
		memoryBudget   = flag.Int64("memory-budget", 0, "Memory budget in MB (0 = auto-detect)")
		autoReserve    = flag.Bool("auto-reserve", true, "Auto-reserve 80% of system resources")
	)
	flag.Parse()

	log.Printf("Starting Dockyard Worker...")

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
		ServerURL:      *serverURL,
		Token:          *token,
		UseTLS:         *useTLS,
		Reconnect:      *reconnect,
		ReconnectDelay: 5 * time.Second,
		DockerService:  dockerService,
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
