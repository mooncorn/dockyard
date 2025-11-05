package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mooncorn/dockyard/worker/internal/client"
)

func main() {
	// Command line flags
	var (
		serverURL = flag.String("server", "localhost:8080", "gRPC server URL to connect to")
		token     = flag.String("token", "worker-token-123", "Authentication token")
		useTLS    = flag.Bool("tls", false, "Use TLS connection")
		reconnect = flag.Bool("reconnect", true, "Enable automatic reconnection")
	)
	flag.Parse()

	log.Printf("Starting Dockyard Worker...")

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
	err := grpcClient.Connect()
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
