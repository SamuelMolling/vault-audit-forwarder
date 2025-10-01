package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

var filePath = os.Getenv("AUDIT_FILE_PATH")

// Check if this Vault instance is the leader
func isVaultLeader() bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:8200/v1/sys/leader")
	if err != nil {
		printLog(LogEvent{
			Type:    "forwarder",
			Level:   "warn",
			Message: fmt.Sprintf("failed to check vault leader status: %v", err),
		}, false)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	var leader struct {
		IsSelf bool `json:"is_self"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&leader); err != nil {
		return false
	}

	return leader.IsSelf
}

func check(err error) {
	if err != nil {
		printLog(LogEvent{
			Type:    "forwarder",
			Level:   "fatal",
			Message: err.Error(),
		}, true)
		os.Exit(1)
	}
}

type LogEvent struct {
	Type    string      `json:"type"`
	Level   string      `json:"level,omitempty"`
	Message interface{} `json:"message"`
}

// Print as JSON to stdout or stderr
func printLog(event LogEvent, isError bool) {
	data, err := json.Marshal(event)
	if err != nil {
		data = []byte(fmt.Sprintf(`{"type":"forwarder","level":"error","message":"marshal error: %v"}`, err))
	}
	if isError {
		fmt.Fprintln(os.Stderr, string(data))
	} else {
		fmt.Fprintln(os.Stdout, string(data))
	}
}

func forwardWithRetry(data []byte, maxRetries int, baseDelay time.Duration) error {
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err = forward(data)
		if err == nil {
			return nil
		}
		wait := baseDelay * (1 << attempt)
		printLog(LogEvent{
			Type:    "forwarder",
			Level:   "error",
			Message: fmt.Sprintf("retry %d/%d failed, waiting %s: %v", attempt+1, maxRetries, wait, err),
		}, true)
		time.Sleep(wait)
	}
	return fmt.Errorf("failed after %d retries: %w", maxRetries, err)
}

func forward(data []byte) error {
	var msg interface{}

	// Try to unmarshal as JSON to embed native JSON in the event
	if err := json.Unmarshal(data, &msg); err != nil {
		// Fallback to string if JSON unmarshal fails
		msg = string(data)
	}

	printLog(LogEvent{
		Type:    "audit",
		Message: msg,
	}, false)
	return nil
}

func logHandler() {
	data, err := os.ReadFile(filePath)
	if err != nil {
		printLog(LogEvent{
			Type:    "forwarder",
			Level:   "warn",
			Message: fmt.Sprintf("audit file not available (likely standby node): %v", err),
		}, false)
		return
	}

	if len(data) == 0 {
		return
	}

	if err := forwardWithRetry(data, 3, 500*time.Millisecond); err != nil {
		printLog(LogEvent{
			Type:    "forwarder",
			Level:   "error",
			Message: fmt.Sprintf("permanent failure forwarding logs: %v", err),
		}, true)
		return
	}

	if err := os.Truncate(filePath, 0); err != nil {
		printLog(LogEvent{
			Type:    "forwarder",
			Level:   "error",
			Message: fmt.Sprintf("error truncating audit file: %v", err),
		}, true)
	}
}

func watchFile(ctx context.Context) {

	// Check if this is the leader node
	if !isVaultLeader() {
		printLog(LogEvent{
			Type:    "forwarder",
			Level:   "info",
			Message: "vault standby node detected - waiting for leadership",
		}, false)

		// Wait and periodically check for leadership
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				printLog(LogEvent{
					Type:    "forwarder",
					Level:   "info",
					Message: "watchFile shutting down gracefully",
				}, false)
				return
			case <-ticker.C:
				if isVaultLeader() {
					printLog(LogEvent{
						Type:    "forwarder",
						Level:   "info",
						Message: "vault leadership acquired - starting audit processing",
					}, false)
					goto startWatching
				}
			}
		}
	}

startWatching:

	watcher, err := fsnotify.NewWatcher()
	check(err)
	defer watcher.Close()

	// Process any existing audit entries in the file before starting the watcher
	printLog(LogEvent{
		Type:    "forwarder",
		Level:   "info",
		Message: "processing existing audit entries at startup",
	}, false)
	logHandler()

	err = watcher.Add(filePath)
	if err != nil {
		printLog(LogEvent{
			Type:    "forwarder",
			Level:   "warn",
			Message: fmt.Sprintf("cannot watch audit file (likely standby node): %v", err),
		}, false)
		// Continue without watcher - just keep checking periodically
		for {
			select {
			case <-ctx.Done():
				printLog(LogEvent{
					Type:    "forwarder",
					Level:   "info",
					Message: "watchFile shutting down gracefully",
				}, false)
				return
			case <-time.After(30 * time.Second):
				logHandler()
			}
		}
	}

	// Check leadership periodically
	leadershipTicker := time.NewTicker(30 * time.Second)
	defer leadershipTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			printLog(LogEvent{
				Type:    "forwarder",
				Level:   "info",
				Message: "watchFile shutting down gracefully",
			}, false)
			return
		case <-leadershipTicker.C:
			if !isVaultLeader() {
				printLog(LogEvent{
					Type:    "forwarder",
					Level:   "info",
					Message: "leadership lost - stopping audit processing",
				}, false)
				return // Exit and restart the function
			}
		case event := <-watcher.Events:
			if event.Op&fsnotify.Write == fsnotify.Write {
				logHandler()
			}
		case err := <-watcher.Errors:
			printLog(LogEvent{
				Type:    "forwarder",
				Level:   "error",
				Message: fmt.Sprintf("watcher error: %v", err),
			}, true)
		}
	}
}

func healthServer(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	server := &http.Server{
		Addr: ":8080",
	}

	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	printLog(LogEvent{
		Type:    "forwarder",
		Level:   "info",
		Message: "health server listening on :8080",
	}, false)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			printLog(LogEvent{
				Type:    "forwarder",
				Level:   "error",
				Message: fmt.Sprintf("health server error: %v", err),
			}, true)
		}
	}()

	<-ctx.Done()

	printLog(LogEvent{
		Type:    "forwarder",
		Level:   "info",
		Message: "health server shutting down gracefully",
	}, false)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		printLog(LogEvent{
			Type:    "forwarder",
			Level:   "error",
			Message: fmt.Sprintf("health server shutdown error: %v", err),
		}, true)
	}
}

func main() {
	if filePath == "" {
		log.Fatal(`{"type":"forwarder","level":"fatal","message":"AUDIT_FILE_PATH not set"}`)
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// WaitGroup to wait for goroutines to finish
	var wg sync.WaitGroup

	// Start health server
	wg.Add(1)
	go healthServer(ctx, &wg)

	// Start file watcher with restart capability
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				watchFile(ctx)
				// If watchFile returns, wait a bit before restarting
				time.Sleep(5 * time.Second)
			}
		}
	}()

	printLog(LogEvent{
		Type:    "forwarder",
		Level:   "info",
		Message: "vault-audit-forwarder started successfully",
	}, false)

	// Wait for termination signal
	sig := <-sigChan
	printLog(LogEvent{
		Type:    "forwarder",
		Level:   "info",
		Message: fmt.Sprintf("received signal %v, initiating graceful shutdown", sig),
	}, false)

	// Cancel context to signal goroutines to stop
	cancel()

	// Wait for all goroutines to finish
	wg.Wait()

	printLog(LogEvent{
		Type:    "forwarder",
		Level:   "info",
		Message: "vault-audit-forwarder shutdown complete",
	}, false)
}
