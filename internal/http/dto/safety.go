package dto

import "time"

// CreateReviewRequest is POST /v1/trips/{id}/reviews.
type CreateReviewRequest struct {
	ToUserID string  `json:"to_user_id" validate:"required,uuid"`
	Rating   int     `json:"rating" validate:"required,min=1,max=5"`
	Comment  *string `json:"comment" validate:"omitempty,max=500"`
}

// ReviewAuthor is the reviewer. Phone numbers are never included.
type ReviewAuthor struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	PhotoURL *string `json:"photo_url"`
}

// Review is one rating.
type Review struct {
	ID        string       `json:"id"`
	TripID    string       `json:"trip_id"`
	From      ReviewAuthor `json:"from_user"`
	ToUserID  string       `json:"to_user_id"`
	Rating    int          `json:"rating"`
	Comment   *string      `json:"comment"`
	CreatedAt time.Time    `json:"created_at"`
}

// ReviewPage is GET /v1/users/{id}/reviews.
type ReviewPage struct {
	Reviews  []Review `json:"reviews"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
	HasMore  bool     `json:"has_more"`
}

// Block is one blocked user. Phone numbers are never included.
type Block struct {
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	PhotoURL  *string   `json:"photo_url"`
	CreatedAt time.Time `json:"created_at"`
}

// BlockPage is GET /v1/me/blocks.
type BlockPage struct {
	Blocks   []Block `json:"blocks"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
	HasMore  bool    `json:"has_more"`
}

// CreateReportRequest is POST /v1/reports.
type CreateReportRequest struct {
	ReportedUserID string  `json:"reported_user_id" validate:"required,uuid"`
	TripID         *string `json:"trip_id" validate:"omitempty,uuid"`
	Reason         string  `json:"reason" validate:"required,oneof=spam harassment scam unsafe_driving no_show other"`
	Details        *string `json:"details" validate:"omitempty,max=1000"`
}

// Report is one filed report.
type Report struct {
	ID             string    `json:"id"`
	ReporterID     string    `json:"reporter_id"`
	ReportedUserID string    `json:"reported_user_id"`
	TripID         *string   `json:"trip_id"`
	Reason         string    `json:"reason"`
	Details        *string   `json:"details"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// ReportPage is GET /admin/v1/reports.
type ReportPage struct {
	Reports  []Report `json:"reports"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
	HasMore  bool     `json:"has_more"`
}

// UpdateReportRequest is PATCH /admin/v1/reports/{id}.
type UpdateReportRequest struct {
	Status string `json:"status" validate:"required,oneof=open reviewing resolved dismissed"`
}

// ReadNotificationsRequest is POST /v1/me/notifications/read.
// all marks every unread row. Otherwise ids is required.
type ReadNotificationsRequest struct {
	IDs []string `json:"ids" validate:"omitempty,dive,uuid"`
	All bool     `json:"all"`
}

// Notification is one inbox row. Title and body follow the recipient's locale.
type Notification struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Payload   map[string]string `json:"payload"`
	ReadAt    *time.Time        `json:"read_at"`
	CreatedAt time.Time         `json:"created_at"`
}

// NotificationPage is GET /v1/me/notifications.
type NotificationPage struct {
	Notifications []Notification `json:"notifications"`
	UnreadCount   int            `json:"unread_count"`
	NextCursor    string         `json:"next_cursor,omitempty"`
}

// UnreadCount is POST /v1/me/notifications/read.
type UnreadCount struct {
	UnreadCount int `json:"unread_count"`
}

// UserModeration is the admin block response.
type UserModeration struct {
	ID        string `json:"id"`
	IsBlocked bool   `json:"is_blocked"`
}

// SetLocationRequest is PATCH /admin/v1/locations/{id}.
type SetLocationRequest struct {
	IsActive *bool `json:"is_active" validate:"required"`
}

// LocationState is a location after an admin activation change.
type LocationState struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	IsActive bool   `json:"is_active"`
}

// GatewayHealth is one SMS gateway. The API key is never included.
type GatewayHealth struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	IsActive         bool       `json:"is_active"`
	Healthy          bool       `json:"healthy"`
	LastHeartbeatAt  *time.Time `json:"last_heartbeat_at"`
	OperatorPrefixes []string   `json:"operator_prefixes"`
	CreatedAt        time.Time  `json:"created_at"`
}

// GatewayList is GET /admin/v1/gateways.
type GatewayList struct {
	Gateways []GatewayHealth `json:"gateways"`
}

// SMSStats is GET /admin/v1/sms/stats.
type SMSStats struct {
	Pending    int `json:"pending"`
	Processing int `json:"processing"`
	Sent       int `json:"sent"`
	Failed     int `json:"failed"`
}
