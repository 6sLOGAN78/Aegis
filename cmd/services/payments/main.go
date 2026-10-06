package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

type Payment struct {
	ID     string  `json:"id"`
	Amount float64 `json:"amount"`
	Status string  `json:"status"`
}

var samplePayments = []Payment{
	{ID: "pay_201", Amount: 99.99, Status: "completed"},
	{ID: "pay_202", Amount: 150.00, Status: "pending"},
}

func HandlePayments(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(samplePayments)
	case http.MethodPost:
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":     "processed",
			"payment_id": "pay_201",
		})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/payments", HandlePayments)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	log.Printf("Payments microservice starting on :%s", port)
	if err := http.ListenAndServe(":"+port, Routes()); err != nil {
		log.Fatalf("Payments microservice failed: %v", err)
	}
}
