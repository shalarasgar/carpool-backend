// Package httperr writes the API error envelope.
package httperr

import (
	"encoding/json"
	"net/http"
)

// Envelope is the standard error body: {"error":{"code":"...","message":"..."}}.
type Envelope struct {
	Error Body `json:"error"`
}

// Body is the machine-readable code plus a human message.
type Body struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Write sends a JSON error envelope.
func Write(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, Envelope{Error: Body{Code: code, Message: message}})
}

// WriteJSON sends a JSON response.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
