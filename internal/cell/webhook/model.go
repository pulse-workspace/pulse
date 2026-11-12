package webhook

import "time"

// Webhook represents an external HTTP endpoint registered to receive platform events.
type Webhook struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	URL       string    `json:"url"`
	Secret    string    `json:"-"` // Never expose secret in JSON responses
	Events    []string  `json:"events"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EventPayload represents the standard JSON envelope delivered to external webhook endpoints.
type EventPayload struct {
	DeliveryID string         `json:"delivery_id"`
	Event      string         `json:"event"`
	TenantID   string         `json:"tenant_id"`
	Timestamp  int64          `json:"timestamp"`
	Data       map[string]any `json:"data"`
}

