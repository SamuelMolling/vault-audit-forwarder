# Build stage
FROM golang:1.25.1-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o vault-audit-forwarder .

# Runtime stage (Distroless, mais seguro que Alpine)
FROM gcr.io/distroless/base-debian12

WORKDIR /bin
COPY --from=builder /src/vault-audit-forwarder .

USER nonroot:nonroot

ENV AUDIT_FILE_PATH=/vault/logs/audit.log

ENTRYPOINT ["/bin/vault-audit-forwarder"]