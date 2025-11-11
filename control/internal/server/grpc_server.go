package server

import (
	"context"
	"errors"
	"io"
	"log"

	"github.com/mooncorn/dockyard/control/internal/services"
	"github.com/mooncorn/dockyard/proto/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type GRPCServer struct {
	pb.UnimplementedDockyardServiceServer
	workerService *services.WorkerService
	jobService    *services.JobService
}

type GRPCServerConfig struct {
	WorkerService *services.WorkerService
	JobService    *services.JobService
}

func NewGRPCServer(config GRPCServerConfig) *GRPCServer {
	return &GRPCServer{
		workerService: config.WorkerService,
		jobService:    config.JobService,
	}
}

func (g *GRPCServer) StreamCommunication(stream pb.DockyardService_StreamCommunicationServer) error {
	// Extract token from stream context
	md, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}

	workerTokens := md.Get("token")
	if len(workerTokens) == 0 {
		return status.Error(codes.Unauthenticated, "missing auth token")
	}

	workerID, err := g.workerService.ConnectWorker(workerTokens[0], stream)
	if err != nil {
		return status.Error(codes.Unauthenticated, "invalid credentials")
	}

	// Cleanup on disconnect
	defer func() {
		g.workerService.DisconnectWorker(workerID)
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
			// Check pong containers and auto-complete pending jobs
			err := g.jobService.HandlePongReceived(workerID, msg.Pong)
			if err != nil {
				log.Printf("Failed to process pong for job completion (worker %s): %v", workerID, err)
			}
		case *pb.WorkerMessage_Metadata:
			// Update worker metadata via service layer (includes validation)
			err := g.workerService.HandleMetadataUpdate(workerID, msg.Metadata)
			if err != nil {
				log.Printf("Failed to update worker %s metadata: %v", workerID, err)
			} else {
				log.Printf("Updated metadata for worker %s", workerID)
			}
		case *pb.WorkerMessage_JobResponse:
			// Handle job completion/failure response from worker
			err := g.jobService.HandleJobResponse(msg.JobResponse)
			if err != nil {
				log.Printf("Failed to process job response from worker %s: %v", workerID, err)
			}
		default:
			log.Printf("Unknown message type received from agent %s: %T", workerID, msg)
		}
	}

	return nil
}
