package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/mooncorn/dockyard/control/internal/auth"
	"github.com/mooncorn/dockyard/control/internal/services"
	"github.com/mooncorn/dockyard/proto/pb"
)

type GRPCServer struct {
	pb.UnimplementedDockyardServiceServer
	authenticator auth.Authenticator
	workerService services.WorkerService
}

type GRPCServerConfig struct {
	Authenticator auth.Authenticator
	WorkerService services.WorkerService
}

func NewGRPCServer(config GRPCServerConfig) *GRPCServer {
	return &GRPCServer{
		authenticator: config.Authenticator,
		workerService: config.WorkerService,
	}
}

func (g *GRPCServer) StreamCommunication(stream pb.DockyardService_StreamCommunicationServer) error {
	// Authenticate worker from stream context
	workerID, err := g.authenticator.Authenticate(stream.Context())
	if err != nil {
		return fmt.Errorf("authentication failed")
	}

	// Check if worker is already connected
	if g.workerService.IsWorkerOnline(workerID) {
		log.Printf("Worker %s rejected: already connected", workerID)
		return fmt.Errorf("worker %s is already connected", workerID)
	}

	log.Printf("Worker %s connected via communication stream", workerID)

	// Register worker with the service
	err = g.workerService.RegisterWorker(workerID, stream)
	if err != nil {
		return fmt.Errorf("failed to register worker: %w", err)
	}

	// Cleanup on disconnect
	defer func() {
		g.workerService.UnregisterWorker(workerID)
		log.Printf("Worker %s disconnected from communication stream", workerID)
	}()

	// Create context that cancels when the stream is done
	streamCtx, streamCancel := context.WithCancel(stream.Context())
	defer streamCancel()

	// Handle incoming messages from worker
	for {
		// Check if context is cancelled
		if streamCtx.Err() != nil {
			if errors.Is(streamCtx.Err(), context.Canceled) {
				log.Printf("\nstream context cancelled for worker: %s", workerID)
			} else {
				log.Printf("\nstream context error for worker %s: %v", workerID, streamCtx.Err())
			}
			return streamCtx.Err()
		}

		workerMsg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				log.Printf("\nWorker %s closed communication stream", workerID)
				break
			}
			log.Printf("\nError receiving message from worker %s: %v", workerID, err)
			return err
		}

		// Handle different message types
		switch msg := workerMsg.Message.(type) {
		case *pb.WorkerMessage_Pong:
			g.workerService.HandlePongReceived(workerID, msg.Pong)
		case *pb.WorkerMessage_Metadata:
			// Update worker metadata via service layer (includes validation)
			err := g.workerService.HandleMetadataUpdate(workerID, msg.Metadata)
			if err != nil {
				log.Printf("Failed to update worker %s metadata: %v", workerID, err)
			} else {
				log.Printf("Updated metadata for worker %s", workerID)
			}
		default:
			log.Printf("Unknown message type received from agent %s: %T", workerID, msg)
		}
	}

	return nil
}
