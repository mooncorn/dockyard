package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mooncorn/dockyard/control/internal/models"
	"github.com/mooncorn/dockyard/control/internal/services"
)

type ContainerHandler struct {
	containerService services.ContainerService
}

func NewContainerHandler(containerService services.ContainerService) *ContainerHandler {
	return &ContainerHandler{
		containerService: containerService,
	}
}

type CreateContainerResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func (h *ContainerHandler) CreateContainer(c *fiber.Ctx) error {
	// Parse request body
	var req models.CreateContainerRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Error: "invalid request body: " + err.Error(),
		})
	}

	// Create container job
	jobID, err := h.containerService.CreateContainer(&req)
	if err != nil {
		// Determine status code based on error type
		statusCode := fiber.StatusInternalServerError
		if err.Error() == "no workers online" || err.Error() == "no suitable worker found with sufficient resources" {
			statusCode = fiber.StatusServiceUnavailable
		} else if err.Error() == "image is required" ||
			err.Error() == "cpu_cores must be greater than 0" ||
			err.Error() == "memory_mb must be greater than 0" {
			statusCode = fiber.StatusBadRequest
		}

		return c.Status(statusCode).JSON(ErrorResponse{
			Error: err.Error(),
		})
	}

	// Return success response
	return c.Status(fiber.StatusAccepted).JSON(CreateContainerResponse{
		JobID:  jobID,
		Status: models.JobStatusPending,
	})
}
