package utils

import (
	"encoding/json"
	"net/http"
)

type ResponseEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func WriteJSON(writer http.ResponseWriter, status int, envelope ResponseEnvelope) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(envelope)
}
