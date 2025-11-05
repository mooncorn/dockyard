package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/mooncorn/dockyard/control/internal/auth"
	"github.com/mooncorn/dockyard/control/internal/registry"
	"github.com/mooncorn/dockyard/control/internal/server"
	"github.com/mooncorn/dockyard/proto/pb"
	"google.golang.org/grpc"
)

func main() {
	// Command line flags
	var (
		port = flag.String("port", "8080", "Port to run the gRPC server on")
		host = flag.String("host", "localhost", "Host to bind the gRPC server to")
	)
	flag.Parse()

	log.Printf("Starting Dockyard Control Server...")

	// Create listener
	address := fmt.Sprintf("%s:%s", *host, *port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", address, err)
	}

	// Create gRPC server
	grpcServer := grpc.NewServer()

	// Create authenticator (simple token-based for now)
	authenticator := auth.NewDefaultAuthenticator(map[string]string{
		"worker-token-123": "worker-1",
		"worker-token-456": "worker-2",
	})

	// Create worker registry
	workerRegistry := registry.NewWorkerRegistry()

	// Register example observer
	observer := &LoggingObserver{}
	workerRegistry.Subscribe(observer)

	// Create and register Dockyard service
	dockyardServer := server.NewGRPCServer(server.GRPCServerConfig{
		Authenticator:  authenticator,
		WorkerRegistry: workerRegistry,
	})

	pb.RegisterDockyardServiceServer(grpcServer, dockyardServer)

	// Start server in goroutine
	go func() {
		log.Printf("gRPC server listening on %s", address)
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve gRPC server: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Printf("Received shutdown signal, stopping server...")

	// Shutdown worker registry first (stops ping loops and closes connections)
	workerRegistry.Shutdown()
	grpcServer.GracefulStop()
	log.Printf("Server stopped gracefully")
}

// LoggingObserver is an example observer that logs worker status changes
type LoggingObserver struct{}

func (o *LoggingObserver) OnEvent(event registry.StatusChangedEvent) {
	log.Printf("🔄 Worker Status Change: %s (%s -> %s)",
		event.WorkerID, event.PreviousStatus, event.CurrentStatus)
}
