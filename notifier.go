package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// NotifierConfig holds notification configuration
type NotifierConfig struct {
	// Enable notification feature
	Enabled bool

	// Environment name (staging, production, etc.)
	Environment string

	// Secret paths to monitor (e.g., ["secret/", "kv/"])
	SecretPaths []string

	// Operations to monitor (create, update, delete)
	Operations []string

	// Include read operations
	IncludeReads bool

	// Slack configuration
	SlackEnabled    bool
	SlackWebhookURL string
	SlackChannel    string
	SlackUsername   string
	SlackIconEmoji  string

	// Webhook configuration
	WebhookEnabled bool
	WebhookURL     string
	WebhookMethod  string
}

// LoadNotifierConfig loads notification configuration from environment
func LoadNotifierConfig() *NotifierConfig {
	config := &NotifierConfig{
		Enabled:        getEnvBool("NOTIFIER_ENABLED", false),
		Environment:    getEnv("ENVIRONMENT", "unknown"),
		SecretPaths:    getEnvSlice("NOTIFIER_SECRET_PATHS", []string{"secret/", "kv/"}),
		Operations:     getEnvSlice("NOTIFIER_OPERATIONS", []string{"create", "update", "delete"}),
		IncludeReads:   getEnvBool("NOTIFIER_INCLUDE_READS", false),
		SlackUsername:  getEnv("NOTIFIER_SLACK_USERNAME", "Vault Audit"),
		SlackIconEmoji: getEnv("NOTIFIER_SLACK_ICON", ":lock:"),
		WebhookMethod:  getEnv("NOTIFIER_WEBHOOK_METHOD", "POST"),
	}

	// Slack configuration
	if slackWebhook := os.Getenv("NOTIFIER_SLACK_WEBHOOK_URL"); slackWebhook != "" {
		config.SlackEnabled = true
		config.SlackWebhookURL = slackWebhook
		config.SlackChannel = os.Getenv("NOTIFIER_SLACK_CHANNEL")
		config.Enabled = true
	}

	// Webhook configuration
	if webhookURL := os.Getenv("NOTIFIER_WEBHOOK_URL"); webhookURL != "" {
		config.WebhookEnabled = true
		config.WebhookURL = webhookURL
		config.Enabled = true
	}

	return config
}

// ProcessAuditEvent processes an audit event and sends notifications if needed
func ProcessAuditEvent(data []byte, config *NotifierConfig) {
	if !config.Enabled {
		return
	}

	// Parse audit event
	var event VaultAuditEvent
	if err := json.Unmarshal(data, &event); err != nil {
		// Not a valid audit event, skip
		return
	}

	// Check if event matches criteria
	if !shouldNotify(&event, config) {
		return
	}

	// Build notification message
	notification := buildNotification(&event, config)

	// Send to configured handlers
	if config.SlackEnabled {
		go sendToSlack(notification, config)
	}

	if config.WebhookEnabled {
		go sendToWebhook(notification, config)
	}
}

// shouldNotify checks if event matches notification criteria
func shouldNotify(event *VaultAuditEvent, config *NotifierConfig) bool {
	// Only process response events (skip request events to avoid duplicates)
	if event.Type != "response" {
		return false
	}

	if event.Request == nil {
		return false
	}

	// Check operation
	operation := event.Request.Operation
	if !contains(config.Operations, operation) {
		return false
	}

	// Skip reads if not included
	if operation == "read" && !config.IncludeReads {
		return false
	}

	// Check if path matches monitored secret paths
	path := event.Request.Path
	for _, prefix := range config.SecretPaths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}

	return false
}

// Notification represents a notification message
type Notification struct {
	Environment string
	SecretPath  string
	User        string
	Operation   string
	Timestamp   time.Time
	RemoteAddr  string
}

// buildNotification creates a notification from an audit event
func buildNotification(event *VaultAuditEvent, config *NotifierConfig) *Notification {
	user := "unknown"
	if event.Auth != nil && event.Auth.DisplayName != "" {
		user = event.Auth.DisplayName
	} else if event.Auth != nil && event.Auth.EntityID != "" {
		user = event.Auth.EntityID
	}

	return &Notification{
		Environment: config.Environment,
		SecretPath:  event.Request.Path,
		User:        user,
		Operation:   event.Request.Operation,
		Timestamp:   event.Time,
		RemoteAddr:  event.Request.RemoteAddress,
	}
}

// retryConfig holds retry configuration
type retryConfig struct {
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
}

// defaultRetryConfig returns default retry configuration
func defaultRetryConfig() retryConfig {
	return retryConfig{
		maxRetries: 3,
		baseDelay:  500 * time.Millisecond,
		maxDelay:   5 * time.Second,
	}
}

// isRetryableStatusCode checks if HTTP status code is retryable
func isRetryableStatusCode(statusCode int) bool {
	// Retry on:
	// - 429 (rate limit)
	// - 500-599 (server errors)
	// - 408 (request timeout)
	return statusCode == 408 || statusCode == 429 || (statusCode >= 500 && statusCode < 600)
}

// retryWithBackoff executes a function with exponential backoff retry
func retryWithBackoff(name string, config retryConfig, fn func() error) error {
	var lastErr error

	for attempt := 0; attempt <= config.maxRetries; attempt++ {
		if attempt > 0 {
			// Calculate exponential backoff with jitter
			delay := config.baseDelay * time.Duration(1<<uint(attempt-1))
			if delay > config.maxDelay {
				delay = config.maxDelay
			}

			printLog(LogEvent{
				Type:    "notifier",
				Level:   "warn",
				Message: fmt.Sprintf("%s: retry attempt %d/%d after %v", name, attempt, config.maxRetries, delay),
			}, false)

			time.Sleep(delay)
		}

		err := fn()
		if err == nil {
			if attempt > 0 {
				printLog(LogEvent{
					Type:    "notifier",
					Level:   "info",
					Message: fmt.Sprintf("%s: succeeded after %d retries", name, attempt),
				}, false)
			}
			return nil
		}

		lastErr = err
	}

	printLog(LogEvent{
		Type:    "notifier",
		Level:   "error",
		Message: fmt.Sprintf("%s: permanent failure after %d attempts: %v", name, config.maxRetries+1, lastErr),
	}, true)

	return lastErr
}

// sendToSlack sends notification to Slack with retry
func sendToSlack(notification *Notification, config *NotifierConfig) {
	// Format timestamp
	timeFormatted := notification.Timestamp.Format("2006-01-02 15:04:05 MST")

	// Build simple message
	slackMsg := map[string]interface{}{
		"username":   config.SlackUsername,
		"icon_emoji": config.SlackIconEmoji,
	}

	if config.SlackChannel != "" {
		slackMsg["channel"] = config.SlackChannel
	}

	// Build fields
	fields := []map[string]string{
		{"type": "mrkdwn", "text": fmt.Sprintf("*Environment:*\n%s", notification.Environment)},
		{"type": "mrkdwn", "text": fmt.Sprintf("*Operation:*\n%s", notification.Operation)},
		{"type": "mrkdwn", "text": fmt.Sprintf("*User:*\n%s", notification.User)},
		{"type": "mrkdwn", "text": fmt.Sprintf("*When:*\n%s", timeFormatted)},
	}

	blocks := []map[string]interface{}{
		{
			"type": "section",
			"text": map[string]string{
				"type": "mrkdwn",
				"text": fmt.Sprintf("*Secret Key:* `%s`", notification.SecretPath),
			},
		},
		{
			"type": "section",
			"fields": fields,
		},
	}

	slackMsg["blocks"] = blocks

	// Marshal payload once (outside retry loop)
	data, err := json.Marshal(slackMsg)
	if err != nil {
		printLog(LogEvent{
			Type:    "notifier",
			Level:   "error",
			Message: fmt.Sprintf("slack: failed to marshal message: %v", err),
		}, true)
		return
	}

	// HTTP client
	client := &http.Client{Timeout: 10 * time.Second}

	// Retry logic
	retryConfig := defaultRetryConfig()
	err = retryWithBackoff("slack", retryConfig, func() error {
		resp, err := client.Post(config.SlackWebhookURL, "application/json", bytes.NewBuffer(data))
		if err != nil {
			return fmt.Errorf("network error: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			if isRetryableStatusCode(resp.StatusCode) {
				return fmt.Errorf("retryable status %d", resp.StatusCode)
			}
			// Non-retryable error (e.g., 400 bad request)
			return fmt.Errorf("non-retryable status %d", resp.StatusCode)
		}

		return nil
	})

	if err == nil {
		printLog(LogEvent{
			Type:    "notifier",
			Level:   "info",
			Message: fmt.Sprintf("slack: notification sent: %s - %s", notification.Operation, notification.SecretPath),
		}, false)
	}
}

// sendToWebhook sends notification to generic webhook with retry
func sendToWebhook(notification *Notification, config *NotifierConfig) {
	payload := map[string]interface{}{
		"environment":  notification.Environment,
		"secret_path":  notification.SecretPath,
		"user":         notification.User,
		"operation":    notification.Operation,
		"timestamp":    notification.Timestamp,
		"remote_addr":  notification.RemoteAddr,
	}

	// Marshal payload once (outside retry loop)
	data, err := json.Marshal(payload)
	if err != nil {
		printLog(LogEvent{
			Type:    "notifier",
			Level:   "error",
			Message: fmt.Sprintf("webhook: failed to marshal payload: %v", err),
		}, true)
		return
	}

	// HTTP client
	client := &http.Client{Timeout: 10 * time.Second}

	// Retry logic
	retryConfig := defaultRetryConfig()
	err = retryWithBackoff("webhook", retryConfig, func() error {
		req, err := http.NewRequest(config.WebhookMethod, config.WebhookURL, bytes.NewBuffer(data))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("network error: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if isRetryableStatusCode(resp.StatusCode) {
				return fmt.Errorf("retryable status %d", resp.StatusCode)
			}
			// Non-retryable error (e.g., 400 bad request, 401 unauthorized)
			return fmt.Errorf("non-retryable status %d", resp.StatusCode)
		}

		return nil
	})

	if err == nil {
		printLog(LogEvent{
			Type:    "notifier",
			Level:   "info",
			Message: fmt.Sprintf("webhook: notification sent: %s - %s", notification.Operation, notification.SecretPath),
		}, false)
	}
}

// Helper functions

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		return strings.Split(value, ",")
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1" || value == "yes"
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
