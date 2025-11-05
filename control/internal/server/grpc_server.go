package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/mooncorn/dockyard/control/internal/auth"
	"github.com/mooncorn/dockyard/control/internal/registry"
	"github.com/mooncorn/dockyard/proto/pb"
)

type GRPCServer struct {
	pb.UnimplementedDockyardServiceServer
	authenticator  auth.Authenticator
	workerRegistry registry.WorkerRegistry
}

type GRPCServerConfig struct {
	Authenticator  auth.Authenticator
	WorkerRegistry registry.WorkerRegistry
}

func NewGRPCServer(config GRPCServerConfig) *GRPCServer {
	return &GRPCServer{
		authenticator:  config.Authenticator,
		workerRegistry: config.WorkerRegistry,
	}
}

func (g *GRPCServer) StreamCommunication(stream pb.DockyardService_StreamCommunicationServer) error {
	// Authenticate worker from stream context
	workerID, err := g.authenticator.Authenticate(stream.Context())
	if err != nil {
		return fmt.Errorf("authentication failed")
	}

	// Check if worker is already connected
	if g.workerRegistry.IsWorkerOnline(workerID) {
		log.Printf("Worker %s rejected: already connected", workerID)
		return fmt.Errorf("worker %s is already connected", workerID)
	}

	log.Printf("Worker %s connected via communication stream", workerID)

	// Register worker with the registry
	err = g.workerRegistry.RegisterWorker(workerID, stream)
	if err != nil {
		return fmt.Errorf("failed to register worker: %w", err)
	}

	// Cleanup on disconnect
	defer func() {
		g.workerRegistry.UnregisterWorker(workerID)
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
			g.workerRegistry.HandlePongReceived(workerID, msg.Pong)
		default:
			log.Printf("Unknown message type received from agent %s: %T", workerID, msg)
		}
	}

	return nil
}
