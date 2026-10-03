package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// LogNotifier prints the approve/reject commands instead of sending a real
// notification - the same "mock first, real plug-in is a drop-in swap"
// pattern as the mock model provider and demo MCP server. The default
// whenever WEBHOOK_URL isn't configured.
type LogNotifier struct {
	ControlAPIBaseURL string
}

func (n *LogNotifier) Notify(_ context.Context, a Approval, snapshot CallSnapshot) error {
	slog.Warn("approval required",
		"approvalID", a.ID,
		"agent", snapshot.AgentID,
		"actingAs", snapshot.ActingAs,
		"action", snapshot.Action,
		"resource", snapshot.ResourceType+"::"+snapshot.ResourceID,
		"expiresAt", a.ExpiresAt,
		"approve", fmt.Sprintf(`curl -X POST %s/approvals/%s/decide -d '{"state":"approved","decidedBy":"you"}'`, n.ControlAPIBaseURL, a.ID),
		"reject", fmt.Sprintf(`curl -X POST %s/approvals/%s/decide -d '{"state":"rejected","decidedBy":"you"}'`, n.ControlAPIBaseURL, a.ID),
	)
	return nil
}

// WebhookNotifier POSTs the approval to a real webhook (e.g. a Slack
// incoming webhook URL) - the actual mechanism the spec calls for, used
// whenever one is configured.
type WebhookNotifier struct {
	URL        string
	HTTPClient *http.Client
}

type webhookPayload struct {
	ApprovalID string       `json:"approvalId"`
	ExpiresAt  time.Time    `json:"expiresAt"`
	Call       CallSnapshot `json:"call"`
}

func (n *WebhookNotifier) Notify(ctx context.Context, a Approval, snapshot CallSnapshot) error {
	body, err := json.Marshal(webhookPayload{ApprovalID: a.ID, ExpiresAt: a.ExpiresAt, Call: snapshot})
	if err != nil {
		return fmt.Errorf("webhook notifier: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook notifier: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := n.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook notifier: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook notifier: webhook returned status %d", resp.StatusCode)
	}
	return nil
}
