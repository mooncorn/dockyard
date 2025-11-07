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
	)
	flag.Parse()

	log.Printf("Starting Dockyard Worker...")

	// Create Docker service
	dockerConfig := service.DockerServiceConfig{
		BaseVolumePath: *volumeBasePath,
		PortRange: &service.PortRange{
			Min: *portRangeMin,
			Max: *portRangeMax,
		},
	}

	dockerService, err := service.NewDockerService(dockerConfig)
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
