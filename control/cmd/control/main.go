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
	"github.com/mooncorn/dockyard/control/internal/db"
	"github.com/mooncorn/dockyard/control/internal/handlers"
	"github.com/mooncorn/dockyard/control/internal/registry"
	"github.com/mooncorn/dockyard/control/internal/server"
	"github.com/mooncorn/dockyard/control/internal/services"
	"github.com/mooncorn/dockyard/proto/pb"
	"google.golang.org/grpc"
)

func main() {
	// Command line flags
	var (
		grpcPort   = flag.String("grpc-port", "8080", "Port to run the gRPC server on")
		httpPort   = flag.String("http-port", "8081", "Port to run the HTTP API server on")
		host       = flag.String("host", "localhost", "Host to bind the servers to")
		dbPath     = flag.String("db", "./dockyard.db", "Path to SQLite database file")
		migrations = flag.String("migrations", "./migrations", "Path to migrations directory")
	)
	flag.Parse()

	log.Printf("Starting Dockyard Control Server...")

	// Initialize database
	log.Printf("Initializing database at %s...", *dbPath)
	database, err := db.NewDB(db.Config{
		DatabasePath:   *dbPath,
		MigrationsPath: *migrations,
	})
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	// Run migrations
	log.Printf("Running database migrations...")
	if err := db.RunMigrations(db.Config{
		DatabasePath:   *dbPath,
		MigrationsPath: *migrations,
	}); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Create store with repositories
	store := db.NewStore(database)
	log.Printf("Database initialized successfully")

	// Create database authenticator
	authenticator := auth.NewDBAuthenticator(store.WorkerRepo)

	// Create worker registry
	workerRegistry := registry.NewWorkerRegistry()

	// Register example observer
	observer := &LoggingObserver{}
	workerRegistry.Subscribe(observer)

	// Create worker service with dependencies
	workerService := services.NewWorkerService(services.WorkerServiceConfig{
		WorkerRegistry: workerRegistry,
		WorkerRepo:     store.WorkerRepo,
	})

	// Create gRPC listener
	grpcAddress := fmt.Sprintf("%s:%s", *host, *grpcPort)
	listener, err := net.Listen("tcp", grpcAddress)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", grpcAddress, err)
	}

	// Create gRPC server
	grpcServer := grpc.NewServer()

	// Create and register Dockyard service
	dockyardServer := server.NewGRPCServer(server.GRPCServerConfig{
		Authenticator: authenticator,
		WorkerService: workerService,
	})

	pb.RegisterDockyardServiceServer(grpcServer, dockyardServer)

	// Start gRPC server in goroutine
	go func() {
		log.Printf("gRPC server listening on %s", grpcAddress)
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve gRPC server: %v", err)
		}
	}()

	// Create HTTP server
	tokenHandler := handlers.NewTokenHandler(workerService)
	httpServer := server.NewHTTPServer(server.HTTPServerConfig{
		TokenHandler: tokenHandler,
		Port:         parsePort(*httpPort),
	})

	// Start HTTP server in goroutine
	go func() {
		if err := httpServer.Start(); err != nil {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Printf("Received shutdown signal, stopping servers...")

	// Shutdown HTTP server
	if err := httpServer.Shutdown(); err != nil {
		log.Printf("Error shutting down HTTP server: %v", err)
	}

	// Shutdown worker registry (stops ping loops and closes connections)
	workerRegistry.Shutdown()

	// Stop gRPC server
	grpcServer.GracefulStop()

	log.Printf("Servers stopped gracefully")
}

// parsePort converts a port string to int
func parsePort(port string) int {
	var p int
	fmt.Sscanf(port, "%d", &p)
	if p == 0 {
		p = 8081 // Default HTTP port
	}
	return p
}

// LoggingObserver is an example observer that logs worker status changes
type LoggingObserver struct{}

func (o *LoggingObserver) OnEvent(event registry.StatusChangedEvent) {
	log.Printf("🔄 Worker Status Change: %s (%s -> %s)",
		event.WorkerID, event.PreviousStatus, event.CurrentStatus)
}
