// Package httperr writes the API error envelope.
package httperr

import (
	"encoding/json"
	"net/http"

	"github.com/shalarasgar/carpool-backend/internal/i18n"
)

// Envelope is the standard error body: {"error":{"code":"...","message":"..."}}.
type Envelope struct {
	Error Body `json:"error"`
}

// Body is the machine-readable code plus a human message. Details carries
// optional field-level validation hints.
type Body struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// Write sends a JSON error envelope with an explicit message.
func Write(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, Envelope{Error: Body{Code: code, Message: message}})
}

// WriteCode sends an error envelope whose message is localized for the request.
func WriteCode(w http.ResponseWriter, r *http.Request, status int, code string) {
	WriteCodeDetails(w, r, status, code, nil)
}

// WriteCodeDetails is WriteCode with validation details.
func WriteCodeDetails(w http.ResponseWriter, r *http.Request, status int, code string, details map[string]string) {
	locale := i18n.FromContext(r.Context())
	WriteJSON(w, status, Envelope{Error: Body{Code: code, Message: i18n.Message(locale, code), Details: details}})
}

// WriteJSON sends a JSON response.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
