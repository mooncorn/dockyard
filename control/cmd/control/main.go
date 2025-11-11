package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mooncorn/dockyard/control/internal/db"
	"github.com/mooncorn/dockyard/control/internal/handlers"
	"github.com/mooncorn/dockyard/control/internal/job"
	"github.com/mooncorn/dockyard/control/internal/server"
	"github.com/mooncorn/dockyard/control/internal/services"
	"github.com/mooncorn/dockyard/control/internal/worker"
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

	connectionStore := worker.NewConnectionStore()

	pinger := worker.NewPinger(worker.PingerConfig{
		ConnectionStore: connectionStore,
		Interval:        30 * time.Second,
	})

	pinger.Start()

	// Create worker manager
	workerRegistry := worker.NewRegistry(worker.RegistryConfig{
		ConnectionStore: connectionStore,
	})

	// Create worker service with dependencies
	workerService := services.NewWorkerService(services.WorkerServiceConfig{
		WorkerRegistry: workerRegistry,
		WorkerRepo:     store.WorkerRepo,
		Pinger:         pinger,
	})

	// Create job service
	jobService := services.NewJobService(store.JobRepo)

	// Create container service
	containerService := services.NewContainerService(services.ContainerServiceConfig{
		ConnectionStore: connectionStore,
		WorkerRepo:      store.WorkerRepo,
		JobRepo:         store.JobRepo,
	})

	// Create and start scheduler
	scheduler := job.NewScheduler(job.SchedulerConfig{
		JobRepo:         store.JobRepo,
		ConnectionStore: connectionStore,
		PollInterval:    3 * time.Second,
	})

	go scheduler.Start()

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
		WorkerService: workerService,
		JobService:    jobService,
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
	workerHandler := handlers.NewWorkerHandler(workerService)
	containerHandler := handlers.NewContainerHandler(*containerService)
	httpServer := server.NewHTTPServer(server.HTTPServerConfig{
		WorkerHandler:    workerHandler,
		ContainerHandler: containerHandler,
		Port:             parsePort(*httpPort),
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

	// Stop scheduler
	scheduler.Stop()

	// Stop pinger
	pinger.Stop()

	// Shutdown HTTP server
	if err := httpServer.Shutdown(); err != nil {
		log.Printf("Error shutting down HTTP server: %v", err)
	}

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
