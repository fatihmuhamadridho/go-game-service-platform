package view

import (
	"encoding/json"
	"net/http"
)

type responseStatus struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type apiResponse struct {
	Status responseStatus `json:"status"`
	Data   any            `json:"data"`
}

func WriteJSON(w http.ResponseWriter, statusCode int, code, message string, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(apiResponse{Status: responseStatus{Code: code, Message: message}, Data: data})
}
