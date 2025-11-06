package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mooncorn/dockyard/control/internal/models"
	"github.com/mooncorn/dockyard/control/internal/services"
)

type TokenHandler struct {
	workerService services.WorkerService
}

func NewTokenHandler(workerService services.WorkerService) *TokenHandler {
	return &TokenHandler{
		workerService: workerService,
	}
}

type CreateTokenResponse struct {
	WorkerID string `json:"worker_id"`
	Token    string `json:"token"`
}

// CreateToken generates a new access token for a worker
func (h *TokenHandler) CreateToken(c *fiber.Ctx) error {
	// Generate worker ID (UUID v4)
	workerID := uuid.New().String()

	// Generate cryptographically secure random token (32 bytes = 64 hex characters)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		log.Printf("Failed to generate token: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate token",
		})
	}
	token := hex.EncodeToString(tokenBytes)

	// Create worker in database
	worker := &models.Worker{
		ID:        workerID,
		Hostname:  "unknown",
		IPAddress: "0.0.0.0",
		CPUCores:  0,
		RAMMB:     0,
		Token:     token,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := h.workerService.CreateWorker(worker); err != nil {
		log.Printf("Failed to create worker: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create worker",
		})
	}

	log.Printf("Created new worker: ID=%s", workerID)

	// Return worker ID and token
	return c.Status(fiber.StatusCreated).JSON(CreateTokenResponse{
		WorkerID: workerID,
		Token:    token,
	})
}
