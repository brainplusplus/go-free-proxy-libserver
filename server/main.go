package main

import (
	"context"
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

	// ✅ Use PORT from environment (EasyPanel compatible)
	port := util.GetPortFromEnv(8080)

	// Optional: ensure port available (safe for local, ignore error in container)
	if err := util.EnsureAvailable(port); err != nil {
		log.Printf("Warning: %v", err)
	}

	// TTL config
	if v := os.Getenv("TIME_EXPIRED"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
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

	// ✅ Healthcheck endpoint (IMPORTANT for EasyPanel)
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	// Swagger (no auth)
	app.Get("/swagger.json", swaggerJSONHandler)
	app.Get("/swagger", swaggerUIHandler)

	// API with auth
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

	RegisterGET(api, "/proxy/get-working",
		"Get a pre-validated working proxy (fast response)",
		getWorkingProxyHandler,
		freeproxy.FreeProxyParameter{},
		ProxyResponse{},
		true,
	)

	RegisterGET(api, "/proxy/list-working",
		"List all pre-validated working proxies",
		getWorkingProxyListHandler,
		freeproxy.FreeProxyParameter{},
		ProxyListResponse{},
		true,
	)

	RegisterGET(api, "/metrics",
		"Get performance metrics",
		getMetricsHandler,
		nil,
		MetricsResponse{},
		true,
	)

	// ✅ Graceful shutdown handler (non-blocking)
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit

		log.Println("Shutting down server (timeout: 5s)...")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := app.ShutdownWithContext(ctx); err != nil {
			log.Printf("Shutdown error: %v", err)
		}

		log.Println("Server stopped.")
	}()

	// ✅ Start server (BLOCKING)
	log.Printf("Server running on http://localhost:%d", port)
	log.Printf("Swagger UI: http://localhost:%d/swagger", port)

	if err := app.Listen(fmt.Sprintf(":%d", port)); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
