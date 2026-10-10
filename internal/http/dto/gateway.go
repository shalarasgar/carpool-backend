package dto

import "time"

// GatewayMessage is one SMS the gateway must send.
type GatewayMessage struct {
	ID          string    `json:"id"`
	Phone       string    `json:"phone"`
	Message     string    `json:"message"`
	Attempts    int       `json:"attempts"`
	LockedUntil time.Time `json:"locked_until"`
	CreatedAt   time.Time `json:"created_at"`
}

// GatewayPendingResponse is GET /gateway/v1/pending.
type GatewayPendingResponse struct {
	Messages []GatewayMessage `json:"messages"`
}

// GatewayReportRequest is POST /gateway/v1/report.
type GatewayReportRequest struct {
	ID     string `json:"id" validate:"required,uuid"`
	Status string `json:"status" validate:"required,oneof=sent failed"`
	Error  string `json:"error,omitempty" validate:"max=500"`
}

// GatewayReportResponse is the queue state after the report.
type GatewayReportResponse struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Attempts int    `json:"attempts"`
}

// GatewayHeartbeatResponse is POST /gateway/v1/heartbeat.
type GatewayHeartbeatResponse struct {
	Gateway    string    `json:"gateway"`
	ServerTime time.Time `json:"server_time"`
}

// CreateGatewayRequest is POST /admin/v1/gateways.
type CreateGatewayRequest struct {
	Name             string   `json:"name" validate:"required,min=1,max=64"`
	OperatorPrefixes []string `json:"operator_prefixes" validate:"max=16,dive,numeric,min=1,max=8"`
}

// CreateGatewayResponse returns the API key exactly once.
type CreateGatewayResponse struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	APIKey           string    `json:"api_key"`
	OperatorPrefixes []string  `json:"operator_prefixes"`
	CreatedAt        time.Time `json:"created_at"`
}
