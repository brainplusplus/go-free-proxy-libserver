package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/joho/godotenv"

	freeproxy "github.com/brainplusplus/go-free-proxy-libserver"
	"github.com/brainplusplus/go-free-proxy-libserver/util"
)

func main() {
	_ = godotenv.Load()

	port := util.GetPortFromEnv(15000)

	// Ensure port is available (kills existing process if needed)
	if err := util.EnsureAvailable(port); err != nil {
		log.Fatalf("Error: %v", err)
	}

	if v := os.Getenv("TIME_EXPIRED"); v != "" {
		secs, err := strconv.Atoi(v)
		if err == nil && secs > 0 {
			freeproxy.SetTTL(time.Duration(secs) * time.Second)
		}
	}

	apiKey := os.Getenv("API_KEY")

	app := fiber.New(fiber.Config{
		AppName: "FreeProxy API v1",
	})

	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New())

	// Swagger endpoints — no auth required
	app.Get("/swagger.json", swaggerJSONHandler)
	app.Get("/swagger", swaggerUIHandler)

	// Authenticated API routes
	api := app.Group("/api/v1", authMiddleware(apiKey))

	RegisterGET(api, "/proxy/get",
		"Get a single validated working proxy",
		getProxyHandler,
		freeproxy.FreeProxyParameter{},
		ProxyResponse{},
		true,
	)

	RegisterGET(api, "/proxy/list",
		"List all cached proxies (not validated)",
		getProxyListHandler,
		freeproxy.FreeProxyParameter{},
		ProxyListResponse{},
		true,
	)

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		log.Printf("Server running on http://localhost:%d", port)
		log.Printf("Swagger UI: http://localhost:%d/swagger", port)
		if err := app.Listen(fmt.Sprintf(":%d", port)); err != nil {
			log.Printf("Server error: %v", err)
		}
	}()

	<-quit
	log.Println("Shutting down server...")
	if err := app.Shutdown(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	log.Println("Server stopped.")
}
