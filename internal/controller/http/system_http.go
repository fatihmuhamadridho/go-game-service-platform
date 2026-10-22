package controllers

import (
	"encoding/json"
	"net/http"
)

func RegisterSystemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/health", health)
	mux.HandleFunc("GET /api/hello", hello)
}

func hello(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "Hello"})
}

func health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Hello from Golang!",
	})
}
