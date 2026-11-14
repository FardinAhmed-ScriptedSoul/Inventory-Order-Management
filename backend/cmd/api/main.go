package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"

	"github.com/FardinAhmed-ScriptedSoul/Inventory-Order-Management/backend/internal/database"
)

type HealthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

func main() {
	// Load environment variables from the repository root.
	if err := godotenv.Load("../.env"); err != nil {
		log.Printf("Could not load .env file: %v", err)
	}

	// Read database connection string.
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	// Establish database connection.
	db, err := database.Connect(databaseURL)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	log.Println("Successfully connected to PostgreSQL")

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)

			json.NewEncoder(w).Encode(HealthResponse{
				Status:   "unhealthy",
				Database: "disconnected",
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		json.NewEncoder(w).Encode(HealthResponse{
			Status:   "healthy",
			Database: "connected",
		})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	log.Printf("StockFlow API starting on port %s", port)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}
}
