# 🔐 Vault Audit Forwarder

A lightweight Go service that watches HashiCorp Vault audit log files and forwards their contents to stdout in structured JSON format.

👉 Designed to run as a sidecar container alongside Vault, making it simple to ship audit logs into observability platforms like Fluent Bit, Loki, ELK, or Datadog.


## ✨ Features
	•	📡 Real-time file watching using fsnotify
	•	📝 Structured JSON output: {"vault_audit": ...}
	•	♻️ Truncates the file after processing to prevent duplicates and disk growth
	•	🚦 Debouncing to avoid multiple reads on a single write event
	•	⚙️ Minimal configuration via environment variable AUDIT_FILE_PATH

## ⚙️ Usage

1. Build locally

```bash
git clone https://github.com/SamuelMolling/vault-audit-forwarder.git
cd vault-audit-forwarder

go build -o vault-audit-forwarder .
```

2. Enable Vault file audit device

```bash
vault audit enable file file_path=/vault/logs/audit.log
```

3. Run as a sidecar in the official HashiCorp Vault Helm Chart
```yaml
server: 
    volumes:
    - name: audit
        emptyDir: {}

    volumeMounts:
    - name: audit
        mountPath: /vault/logs

    extraContainers:
    - name: vault-audit-forwarder
        image: ghcr.io/samuelmolling/vault-audit-forwarder:latest
        imagePullPolicy: IfNotPresent
        env:
        - name: AUDIT_FILE_PATH
            value: /vault/logs/audit.log
        volumeMounts:
        - name: audit
            mountPath: /vault/logs
        ports:
        - containerPort: 8080
            name: healthz
        livenessProbe:
        httpGet:
            path: /healthz
            port: healthz
        initialDelaySeconds: 5
        periodSeconds: 10
        readinessProbe:
        httpGet:
            path: /healthz
            port: healthz
        initialDelaySeconds: 5
        periodSeconds: 10
```

## 🚀 Why?
✅ Avoids Vault blocking when audit file grows full

✅ Standardizes audit logs into your existing logging pipeline

✅ Works natively in Kubernetes with Vault Helm chart

✅ No external dependencies (pure Go binary <10MB)

