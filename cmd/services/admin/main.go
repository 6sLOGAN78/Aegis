package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

type AdminUser struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

var sampleUsers = []AdminUser{
	{ID: "usr_admin_01", Role: "application-admin"},
	{ID: "usr_developer_01", Role: "developer"},
}

func HandleAdminUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(sampleUsers)
	case http.MethodPost:
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "user created",
		})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin/users", HandleAdminUsers)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}

	log.Printf("Admin microservice starting on :%s", port)
	if err := http.ListenAndServe(":"+port, Routes()); err != nil {
		log.Fatalf("Admin microservice failed: %v", err)
	}
}
