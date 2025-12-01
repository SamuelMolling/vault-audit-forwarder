package main

import "time"

// VaultAuditEvent represents a Vault audit log event
type VaultAuditEvent struct {
	Time     time.Time `json:"time"`
	Type     string    `json:"type"`
	Auth     *Auth     `json:"auth,omitempty"`
	Request  *Request  `json:"request,omitempty"`
	Response *Response `json:"response,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type Auth struct {
	ClientToken   string            `json:"client_token,omitempty"`
	Accessor      string            `json:"accessor,omitempty"`
	DisplayName   string            `json:"display_name,omitempty"`
	Policies      []string          `json:"policies,omitempty"`
	TokenPolicies []string          `json:"token_policies,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	EntityID      string            `json:"entity_id,omitempty"`
}

type Request struct {
	ID            string                 `json:"id,omitempty"`
	Operation     string                 `json:"operation,omitempty"` // create, update, delete, read
	Path          string                 `json:"path,omitempty"`
	Data          map[string]interface{} `json:"data,omitempty"`
	RemoteAddress string                 `json:"remote_address,omitempty"`
	Namespace     *Namespace             `json:"namespace,omitempty"`
}

type Namespace struct {
	ID   string `json:"id,omitempty"`
	Path string `json:"path,omitempty"`
}

type Response struct {
	Data map[string]interface{} `json:"data,omitempty"`
}
