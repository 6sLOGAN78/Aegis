package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

type Order struct {
	ID     string `json:"id"`
	Item   string `json:"item"`
	Status string `json:"status"`
}

var sampleOrders = []Order{
	{ID: "ord_101", Item: "Cloud Scanner", Status: "shipped"},
	{ID: "ord_102", Item: "Security Gateway", Status: "processing"},
}

func HandleOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sampleOrders)
}

func Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/orders", HandleOrders)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("Orders microservice starting on :%s", port)
	if err := http.ListenAndServe(":"+port, Routes()); err != nil {
		log.Fatalf("Orders microservice failed: %v", err)
	}
}
