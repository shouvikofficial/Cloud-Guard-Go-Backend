package main

import (
	"context"
	"log"
	"os"

	"backend-go/api"
	"backend-go/tgclient"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found or failed to load")
	}

	// Initialize Telegram client
	ctx := context.Background()
	tg, err := tgclient.NewTGClient(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize Telegram MTProto client: %v", err)
	}
	defer tg.Client.Run(ctx, func(ctx context.Context) error { return nil }) // ensure it shuts down cleanly if needed

	app := fiber.New(fiber.Config{
		AppName: "Telegram Drive Worker (Go)",
	})

	// Inject tg client into fiber context state for routes to access
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("tg", tg)
		return c.Next()
	})

	// Enable CORS
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "*",
		AllowMethods: "*",
	}))

	// Setup Routes
	api.SetupUploadRoutes(app)
	api.SetupDownloadRoutes(app)
	api.SetupDeleteRoutes(app)

	// Basic health check
	app.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "online",
			"info":   "Your Drive Backend (Go) is Ready",
		})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	log.Printf("Server starting on port %s", port)
	if err := app.Listen(":" + port); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
