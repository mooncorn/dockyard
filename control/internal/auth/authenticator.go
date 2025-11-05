package auth

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Authenticator interface for agent authentication
type Authenticator interface {
	Authenticate(ctx context.Context) (string, error)
}

// DefaultAuthenticator uses a static map for authentication (development/testing only)
type DefaultAuthenticator struct {
	allowedWorkers map[string]string // token => id
}

// NewDefaultAuthenticator creates a new default authenticator
func NewDefaultAuthenticator(allowedWorkers map[string]string) *DefaultAuthenticator {
	return &DefaultAuthenticator{
		allowedWorkers: allowedWorkers,
	}
}

// Authenticate validates worker credentials
func (a *DefaultAuthenticator) Authenticate(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing metadata")
	}

	workerTokens := md.Get("token")
	if len(workerTokens) == 0 {
		return "", status.Error(codes.Unauthenticated, "missing auth token")
	}

	workerToken := workerTokens[0]

	workerId, exists := a.allowedWorkers[workerToken]
	if !exists {
		return "", fmt.Errorf("invalid credentials")
	}
	return workerId, nil
}
