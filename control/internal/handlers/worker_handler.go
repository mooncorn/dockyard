package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mooncorn/dockyard/control/internal/services"
)

type WorkerHandler struct {
	workerService *services.WorkerService
}

func NewWorkerHandler(workerService *services.WorkerService) *WorkerHandler {
	return &WorkerHandler{
		workerService: workerService,
	}
}

type CreateTokenResponse struct {
	WorkerID string `json:"worker_id"`
	Token    string `json:"token"`
}

// CreateWorker generates a new access token for a worker
func (h *WorkerHandler) CreateWorker(c *fiber.Ctx) error {
	worker, err := h.workerService.CreateWorker()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create worker",
		})
	}

	// Return worker ID and token
	return c.Status(fiber.StatusCreated).JSON(CreateTokenResponse{
		WorkerID: worker.ID,
		Token:    worker.Token,
	})
}
