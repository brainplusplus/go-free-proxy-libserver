package main

import "github.com/gofiber/fiber/v2"

// authMiddleware validates X-API-Key header or ?api_key= query param.
// If apiKey is empty string, all requests are allowed.
func authMiddleware(apiKey string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if apiKey == "" {
			return c.Next()
		}

		key := c.Get("X-API-Key")
		if key == "" {
			key = c.Query("api_key")
		}

		if key != apiKey {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid or missing API key",
			})
		}

		return c.Next()
	}
}
