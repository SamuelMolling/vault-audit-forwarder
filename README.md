# 🔐 Vault Audit Forwarder

A lightweight Go service that **watches Vault audit log files**, forwards their contents to `stdout` in structured JSON format, and **optionally sends real-time notifications** when secrets are modified.

Designed to run as a **sidecar container** alongside Vault, making it easy to collect audit logs with log forwarders like FluentBit, Loki, ELK, or Datadog.

---

## ✨ Features

### Core Features
- **Real-time Monitoring**: Watches audit log file using `fsnotify`
- **Structured Logging**: Outputs to `stdout` as JSON for log aggregation
- **Vault HA Support**: Automatically detects leader/standby and only processes on leader nodes
- **File Management**: Truncates file after processing to avoid duplicate reads
- **Health Checks**: HTTP health endpoint on `:8080/healthz`
- **Graceful Shutdown**: Proper signal handling and cleanup

### 🚨 Notification Features (NEW!)
- **Slack Notifications**: Rich formatted messages with emoji indicators
- **Generic Webhooks**: Send to any HTTP endpoint (PagerDuty, Datadog, custom APIs)
- **Smart Filtering**: Monitor specific secret paths and operations
- **Configurable**: Enable/disable via environment variables
- **Non-blocking**: Notifications run async without impacting log forwarding

---

## 📋 Quick Start

### Basic Usage (Log Forwarding Only)

```bash
docker run -v /vault/audit:/vault/audit \
  -e AUDIT_FILE_PATH=/vault/audit/audit.log \
  vault-audit-forwarder:latest
```

### With Slack Notifications

```bash
docker run -v /vault/audit:/vault/audit \
  -e AUDIT_FILE_PATH=/vault/audit/audit.log \
  -e NOTIFIER_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/YOUR/WEBHOOK \
  -e NOTIFIER_SLACK_CHANNEL="#vault-alerts" \
  -e NOTIFIER_SECRET_PATHS="secret/,kv/,database/" \
  vault-audit-forwarder:latest
```

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: vault
spec:
  replicas: 3  # HA cluster
  template:
    spec:
      volumes:
        - name: audit-logs
          emptyDir: {}

      containers:
        - name: vault
          image: hashicorp/vault:latest
          volumeMounts:
            - name: audit-logs
              mountPath: /vault/audit

        - name: vault-audit-forwarder
          image: vault-audit-forwarder:latest
          env:
            # Required
            - name: AUDIT_FILE_PATH
              value: /vault/audit/audit.log

            # Slack notifications (optional)
            - name: NOTIFIER_SLACK_WEBHOOK_URL
              valueFrom:
                secretKeyRef:
                  name: vault-secrets
                  key: slack-webhook-url
            - name: NOTIFIER_SLACK_CHANNEL
              value: "#vault-alerts"
            - name: NOTIFIER_SLACK_USERNAME
              value: "Vault Audit"

            # Notification filters
            - name: NOTIFIER_SECRET_PATHS
              value: "secret/,kv/,database/"
            - name: NOTIFIER_OPERATIONS
              value: "create,update,delete"
            - name: NOTIFIER_INCLUDE_READS
              value: "false"

          volumeMounts:
            - name: audit-logs
              mountPath: /vault/audit

          resources:
            requests:
              memory: "64Mi"
              cpu: "50m"
            limits:
              memory: "128Mi"
              cpu: "100m"
```

---

## ⚙️ Configuration

### Required Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `AUDIT_FILE_PATH` | Path to Vault audit log file | `/vault/audit/audit.log` |

### Notification Configuration (Optional)

#### Slack Notifications

| Variable | Description | Default |
|----------|-------------|---------|
| `NOTIFIER_SLACK_WEBHOOK_URL` | Slack webhook URL (enables Slack) | - |
| `NOTIFIER_SLACK_CHANNEL` | Override channel | - |
| `NOTIFIER_SLACK_USERNAME` | Bot username | `Vault Audit` |
| `NOTIFIER_SLACK_ICON` | Bot emoji | `:lock:` |

#### Generic Webhook

| Variable | Description | Default |
|----------|-------------|---------|
| `NOTIFIER_WEBHOOK_URL` | Webhook endpoint (enables webhook) | - |
| `NOTIFIER_WEBHOOK_METHOD` | HTTP method | `POST` |

#### Filtering Options

| Variable | Description | Default |
|----------|-------------|---------|
| `NOTIFIER_SECRET_PATHS` | Comma-separated paths to monitor | `secret/,kv/` |
| `NOTIFIER_OPERATIONS` | Operations to monitor | `create,update,delete` |
| `NOTIFIER_INCLUDE_READS` | Include read operations | `false` |

---

## 📨 Notification Examples

### Slack Message

When a secret is modified, you'll receive a formatted Slack notification:

```
🔴 Vault Secret Delete

User admin performed delete on: secret/prod/database/password

━━━━━━━━━━━━━━━━━━━━━━━━━━
Secret Path: secret/prod/database/password
User: admin
Operation: delete
Time: 2025-11-28T13:45:00Z
Remote Address: 10.0.1.5
```

### Webhook Payload

Generic webhooks receive a JSON payload:

```json
{
  "title": "Vault Secret Update",
  "message": "User admin performed update on: secret/prod/api-key",
  "severity": "info",
  "secret_path": "secret/prod/api-key",
  "user": "admin",
  "operation": "update",
  "timestamp": "2025-11-28T13:45:00Z",
  "metadata": {
    "remote_address": "10.0.1.5",
    "namespace": "production",
    "policies": ["admin", "secrets-write"]
  }
}
```

**Severity Levels:**
- `info`: create, update operations
- `warning`: delete operations
- `critical`: failed operations

---

## 🏗️ Building

```bash
# Build binary
go build -o vault-audit-forwarder .

# Build Docker image
docker build -t vault-audit-forwarder:latest .
```

---

## 🔧 How It Works

### Flow Diagram

```
┌─────────────┐
│   Vault     │ writes audit.log
└──────┬──────┘
       │
       ▼
┌──────────────────────────────────────┐
│  vault-audit-forwarder (sidecar)     │
├──────────────────────────────────────┤
│  1. Watch file (fsnotify)            │
│  2. Read new entries                 │
│  3. Forward to stdout → logs         │
│  4. Check if secret operation        │
│  5. Send notification if matched     │
│  6. Truncate file                    │
└────┬─────────────────────┬───────────┘
     │                     │
     ▼                     ▼
┌─────────────┐      ┌─────────────┐
│   Loki/ELK  │      │    Slack    │
│    (logs)   │      │  (alerts)   │
└─────────────┘      └─────────────┘
```

### Vault HA Behavior

In a Vault HA cluster with 3 nodes:

```
vault-0 (Leader)   → forwarder ACTIVE   ✅ Forwarding + Notifying
vault-1 (Standby)  → forwarder WAITING  ⏸️  Monitoring leadership
vault-2 (Standby)  → forwarder WAITING  ⏸️  Monitoring leadership
```

- Only the **leader node's forwarder** processes events
- Standby nodes wait for leadership
- If leader changes, new leader's forwarder activates
- Prevents duplicate notifications in HA setups

---

## 🎯 Use Cases

### 1. Log Collection Only
Just forward audit logs to your logging system (Loki, ELK, Datadog):
```bash
AUDIT_FILE_PATH=/vault/audit/audit.log
# No NOTIFIER_* variables = notifications disabled
```

### 2. Slack Alerts for Secret Changes
Get notified in Slack when production secrets change:
```bash
AUDIT_FILE_PATH=/vault/audit/audit.log
NOTIFIER_SLACK_WEBHOOK_URL=https://hooks.slack.com/...
NOTIFIER_SECRET_PATHS=secret/prod/,secret/staging/
NOTIFIER_OPERATIONS=create,update,delete
```

### 3. Security Monitoring via Webhook
Send events to your SIEM or security platform:
```bash
AUDIT_FILE_PATH=/vault/audit/audit.log
NOTIFIER_WEBHOOK_URL=https://siem.company.com/vault-events
NOTIFIER_SECRET_PATHS=secret/
```

### 4. Both Logs + Notifications
Combine log collection with real-time alerts:
```bash
AUDIT_FILE_PATH=/vault/audit/audit.log
NOTIFIER_SLACK_WEBHOOK_URL=https://hooks.slack.com/...
NOTIFIER_WEBHOOK_URL=https://your-api.com/events
# Forwarder sends to stdout AND notifications
```

---

## 🤔 FAQ

**Q: Does enabling notifications impact log forwarding?**
A: No! Notifications run asynchronously in goroutines. Log forwarding continues at full speed even if notification endpoints are slow or down.

**Q: What happens if Slack/webhook is unavailable?**
A: The forwarder automatically retries with exponential backoff (3 attempts). Transient errors (network issues, rate limits, 5xx errors) are retried. After all retries fail, the error is logged but the forwarder continues processing. Audit logs are never blocked.

**Q: Can I monitor multiple secret paths?**
A: Yes! Use comma-separated values: `NOTIFIER_SECRET_PATHS=secret/,kv/,database/,pki/`

**Q: How do I disable notifications?**
A: Simply don't set `NOTIFIER_SLACK_WEBHOOK_URL` or `NOTIFIER_WEBHOOK_URL`. The notifier remains disabled.

**Q: Can I get notified for read operations?**
A: Yes, set `NOTIFIER_INCLUDE_READS=true`, but be aware this can be very noisy in production.

---

## 🛠️ Troubleshooting

Check logs for notifier status:
```bash
kubectl logs vault-0 -c vault-audit-forwarder | grep notifier
```

Expected output when enabled:
```json
{"type":"forwarder","level":"info","message":"notification feature enabled"}
{"type":"forwarder","level":"info","message":"slack notifications enabled"}
{"type":"notifier","level":"info","message":"slack: notification sent: delete - secret/prod/db"}
```

Retry behavior on transient failures:
```json
{"type":"notifier","level":"warn","message":"slack: retry attempt 1/3 after 500ms"}
{"type":"notifier","level":"warn","message":"slack: retry attempt 2/3 after 1s"}
{"type":"notifier","level":"info","message":"slack: succeeded after 2 retries"}
```

Permanent failure after all retries:
```json
{"type":"notifier","level":"error","message":"slack: permanent failure after 4 attempts: retryable status 503"}
```
