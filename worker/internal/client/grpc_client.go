package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/mooncorn/dockyard/proto/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type GRPCClientConfig struct {
	ServerURL    string
	Token        string
	UseTLS       bool
	Reconnect    bool
	ReconnectDelay time.Duration
}

type GRPCClient struct {
	config     GRPCClientConfig
	conn       *grpc.ClientConn
	client     pb.DockyardServiceClient
	stream     pb.DockyardService_StreamCommunicationClient
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.RWMutex
	connected  bool

	// Reconnection management
	reconnectMu sync.Mutex
	stopReconnect chan struct{}
}

func NewGRPCClient(config GRPCClientConfig) *GRPCClient {
	// Set default reconnect delay if not specified
	if config.ReconnectDelay == 0 {
		config.ReconnectDelay = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &GRPCClient{
		config:        config,
		ctx:           ctx,
		cancel:        cancel,
		stopReconnect: make(chan struct{}),
	}
}

func (c *GRPCClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return fmt.Errorf("client already connected")
	}

	// Setup gRPC connection options
	var opts []grpc.DialOption

	if c.config.UseTLS {
		// Use TLS credentials
		creds := credentials.NewTLS(&tls.Config{
			ServerName: c.config.ServerURL,
		})
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		// Use insecure connection
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Add authentication interceptor
	opts = append(opts, grpc.WithUnaryInterceptor(c.authUnaryInterceptor))
	opts = append(opts, grpc.WithStreamInterceptor(c.authStreamInterceptor))

	// Establish connection
	conn, err := grpc.DialContext(c.ctx, c.config.ServerURL, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	c.conn = conn
	c.client = pb.NewDockyardServiceClient(conn)

	// Create the stream with authentication context
	authCtx := c.addAuthToContext(c.ctx)
	stream, err := c.client.StreamCommunication(authCtx)
	if err != nil {
		c.conn.Close()
		return fmt.Errorf("failed to create stream: %w", err)
	}

	c.stream = stream
	c.connected = true

	log.Printf("Connected to gRPC server at %s", c.config.ServerURL)

	// Send worker metadata
	go c.sendMetadata()

	// Start message handling
	go c.handleMessages()

	return nil
}

func (c *GRPCClient) sendMetadata() {
	// Collect system metadata
	metadata, err := CollectSystemMetadata()
	if err != nil {
		log.Printf("Failed to collect system metadata: %v", err)
		return
	}

	// Create metadata message
	metadataMsg := &pb.WorkerMessage{
		Message: &pb.WorkerMessage_Metadata{
			Metadata: metadata,
		},
	}

	// Send metadata to server
	c.mu.RLock()
	stream := c.stream
	c.mu.RUnlock()

	if stream != nil {
		err := stream.Send(metadataMsg)
		if err != nil {
			log.Printf("Failed to send metadata: %v", err)
		} else {
			log.Printf("Sent metadata: hostname=%s, ip=%s, cpu_cores=%d, ram_mb=%d",
				metadata.Hostname, metadata.IpAddress, metadata.CpuCores, metadata.RamMb)
		}
	}
}

func (c *GRPCClient) addAuthToContext(ctx context.Context) context.Context {
	if c.config.Token != "" {
		md := metadata.New(map[string]string{
			"token": c.config.Token,
		})
		return metadata.NewOutgoingContext(ctx, md)
	}
	return ctx
}

func (c *GRPCClient) authUnaryInterceptor(
	ctx context.Context,
	method string,
	req, reply interface{},
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	authCtx := c.addAuthToContext(ctx)
	return invoker(authCtx, method, req, reply, cc, opts...)
}

func (c *GRPCClient) authStreamInterceptor(
	ctx context.Context,
	desc *grpc.StreamDesc,
	cc *grpc.ClientConn,
	method string,
	streamer grpc.Streamer,
	opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	authCtx := c.addAuthToContext(ctx)
	return streamer(authCtx, desc, cc, method, opts...)
}

func (c *GRPCClient) handleMessages() {
	defer func() {
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()

		if c.config.Reconnect {
			go c.reconnectLoop()
		}
	}()

	for {
		// Check if context is cancelled
		if c.ctx.Err() != nil {
			log.Printf("Client context cancelled, stopping message handling")
			return
		}

		c.mu.RLock()
		stream := c.stream
		c.mu.RUnlock()

		if stream == nil {
			log.Printf("Stream is nil, stopping message handling")
			return
		}

		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				log.Printf("Server closed the stream")
			} else {
				log.Printf("Error receiving message: %v", err)
			}
			return
		}

		// Handle different message types
		switch message := msg.Message.(type) {
		case *pb.ControlMessage_Ping:
			c.handlePing(message.Ping)
		default:
			log.Printf("Received unknown message type: %T", message)
		}
	}
}

func (c *GRPCClient) handlePing(ping *pb.Ping) {
	log.Printf("Received ping with timestamp: %d", ping.Timestamp)

	// Create pong response
	pong := &pb.WorkerMessage{
		Message: &pb.WorkerMessage_Pong{
			Pong: &pb.Pong{
				Timestamp:     time.Now().UnixNano(),
				PingTimestamp: ping.Timestamp,
			},
		},
	}

	// Send pong response
	c.mu.RLock()
	stream := c.stream
	c.mu.RUnlock()

	if stream != nil {
		err := stream.Send(pong)
		if err != nil {
			log.Printf("Failed to send pong: %v", err)
		} else {
			log.Printf("Sent pong response: ping_ts=%d, pong_ts=%d",
				ping.Timestamp, pong.GetPong().Timestamp)
		}
	}
}

func (c *GRPCClient) reconnectLoop() {
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()

	log.Printf("Starting reconnection loop...")

	ticker := time.NewTicker(c.config.ReconnectDelay)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopReconnect:
			log.Printf("Reconnection loop stopped")
			return
		case <-c.ctx.Done():
			log.Printf("Client context cancelled, stopping reconnection")
			return
		case <-ticker.C:
			c.mu.RLock()
			connected := c.connected
			c.mu.RUnlock()

			if !connected {
				log.Printf("Attempting to reconnect...")

				// Close existing connection if any
				c.closeConnection()

				// Try to reconnect
				err := c.Connect()
				if err != nil {
					log.Printf("Reconnection failed: %v", err)
				} else {
					log.Printf("Successfully reconnected")
					return // Exit reconnection loop
				}
			} else {
				log.Printf("Already connected, stopping reconnection loop")
				return
			}
		}
	}
}

func (c *GRPCClient) closeConnection() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stream != nil {
		c.stream.CloseSend()
		c.stream = nil
	}

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	c.connected = false
}

func (c *GRPCClient) Disconnect() {
	log.Printf("Disconnecting from gRPC server...")

	// Stop reconnection attempts
	close(c.stopReconnect)

	// Cancel context
	c.cancel()

	// Close connection
	c.closeConnection()

	log.Printf("Disconnected from gRPC server")
}

func (c *GRPCClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// SendMessage sends a custom message to the server
func (c *GRPCClient) SendMessage(msg *pb.WorkerMessage) error {
	c.mu.RLock()
	stream := c.stream
	connected := c.connected
	c.mu.RUnlock()

	if !connected || stream == nil {
		return fmt.Errorf("client is not connected")
	}

	return stream.Send(msg)
}