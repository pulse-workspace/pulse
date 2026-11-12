package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// Dispatcher executes outbound HTTP POST deliveries to registered webhook endpoints.
type Dispatcher struct {
	httpClient *http.Client
	log        logger.Logger
}

// NewDispatcher constructs a webhook HTTP dispatcher with 5-second timeout.
func NewDispatcher(log logger.Logger) *Dispatcher {
	return &Dispatcher{
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		log: log.With().Str("component", "webhook_dispatcher").Logger(),
	}
}

// Deliver sends an signed HTTP POST request to the destination webhook URL.
//
// Business Logic:
// 1. Marshals EventPayload into JSON.
// 2. Computes HMAC-SHA256 signature using webhook shared secret.
// 3. Sets X-Pulse-Signature, X-Pulse-Event, X-Pulse-Delivery headers.
// 4. Executes POST request and validates 2xx response status code.
func (d *Dispatcher) Deliver(ctx context.Context, hook *Webhook, payload *EventPayload) error {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	sig, err := SignPayload(bodyBytes, hook.Secret)
	if err != nil {
		return fmt.Errorf("failed to compute webhook HMAC signature: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", hook.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pulse-Signature", sig)
	req.Header.Set("X-Pulse-Event", payload.Event)
	req.Header.Set("X-Pulse-Delivery", payload.DeliveryID)
	req.Header.Set("User-Agent", "Pulse-Webhook-Gateway/1.0")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		d.log.Warn().Err(err).Str("url", hook.URL).Str("event", payload.Event).Msg("webhook delivery failed")
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		d.log.Warn().Int("status", resp.StatusCode).Str("url", hook.URL).Msg("webhook target returned non-2xx status")
		return fmt.Errorf("webhook endpoint returned status %d", resp.StatusCode)
	}

	d.log.Debug().Int("status", resp.StatusCode).Str("url", hook.URL).Str("delivery_id", payload.DeliveryID).Msg("webhook delivered successfully")
	return nil
}

