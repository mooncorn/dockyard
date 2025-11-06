package server

import (
	"fmt"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/mooncorn/dockyard/control/internal/handlers"
)

type HTTPServer struct {
	app          *fiber.App
	tokenHandler *handlers.TokenHandler
	port         int
}

type HTTPServerConfig struct {
	TokenHandler *handlers.TokenHandler
	Port         int
}

func NewHTTPServer(config HTTPServerConfig) *HTTPServer {
	app := fiber.New(fiber.Config{
		AppName: "Dockyard Control API",
	})

	// Middleware
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST,PUT,DELETE,OPTIONS",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
	}))

	return &HTTPServer{
		app:          app,
		tokenHandler: config.TokenHandler,
		port:         config.Port,
	}
}

func (s *HTTPServer) RegisterRoutes() {
	// API routes
	api := s.app.Group("/api")

	// Token management
	tokens := api.Group("/tokens")
	tokens.Post("/", s.tokenHandler.CreateToken)

	// Health check
	s.app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
		})
	})
}

func (s *HTTPServer) Start() error {
	s.RegisterRoutes()

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("Starting HTTP server on %s", addr)

	return s.app.Listen(addr)
}

func (s *HTTPServer) Shutdown() error {
	log.Println("Shutting down HTTP server...")
	return s.app.Shutdown()
}
