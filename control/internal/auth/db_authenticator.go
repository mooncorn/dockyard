package auth

import (
	"context"

	"github.com/mooncorn/dockyard/control/internal/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// DBAuthenticator authenticates workers using database
type DBAuthenticator struct {
	workerRepo db.WorkerRepository
}

// NewDBAuthenticator creates a new database-backed authenticator
func NewDBAuthenticator(workerRepo db.WorkerRepository) *DBAuthenticator {
	return &DBAuthenticator{
		workerRepo: workerRepo,
	}
}

// Authenticate validates worker credentials against the database
func (a *DBAuthenticator) Authenticate(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing metadata")
	}

	workerTokens := md.Get("token")
	if len(workerTokens) == 0 {
		return "", status.Error(codes.Unauthenticated, "missing auth token")
	}

	workerToken := workerTokens[0]

	// Look up worker by token in database
	worker, err := a.workerRepo.GetWorkerByToken(workerToken)
	if err != nil {
		return "", status.Error(codes.Unauthenticated, "invalid credentials")
	}

	return worker.ID, nil
}
