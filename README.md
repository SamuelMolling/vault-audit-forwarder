# 🔐 Vault Audit Forwarder

A lightweight Go service that **watches Vault audit log files** and forwards their contents to `stdout` in structured JSON format.  
Designed to run as a **sidecar container** alongside Vault, making it easy to collect audit logs with log forwarders like FluentBit, Loki, ELK, or Datadog.

---

## ✨ Features
- Watches an audit log file in real time (`fsnotify`).
- Prints new entries to `stdout` as JSON (`{"vault_audit": ...}`).
- Truncates the file after processing (avoids duplicate reads).
- Debounces multiple events to prevent duplicate log output.
- Simple configuration via environment variables.

---

## ⚙️ Usage

### 1. Build locally
```bash
git clone https://github.com/SamuelMolling/vault-audit-forwarder.git
cd vault-audit-forwarder

go build -o vault-audit-forwarder .