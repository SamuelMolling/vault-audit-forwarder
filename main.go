package main

import (
	"log"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
)

var filePath = os.Getenv("AUDIT_FILE_PATH")

// processFile reads the audit file, prints its contents to stdout in JSON format,
// and truncates the file afterwards.
func processFile() {
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("ERROR reading file: %v", err)
		return
	}

	if len(data) == 0 {
		return // nothing new
	}

	// Print as structured JSON (for log parsers like FluentBit, ELK, Loki, etc.)
	log.Printf("{\"vault_audit\": %s}", string(data))

	// Truncate file to avoid re-processing the same logs
	if err := os.Truncate(filePath, 0); err != nil {
		log.Printf("ERROR truncating file: %v", err)
	}
}

func watchFile() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("ERROR creating watcher: %v", err)
	}
	defer watcher.Close()

	events := make(chan struct{}, 1)

	// Worker goroutine to process events with debounce
	go func() {
		for range events {
			time.Sleep(200 * time.Millisecond) // debounce to group multiple writes
			processFile()
		}
	}()

	// Event listener
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op&fsnotify.Write == fsnotify.Write {
					// non-blocking send to avoid piling up
					select {
					case events <- struct{}{}:
					default:
					}
				}
			case err := <-watcher.Errors:
				log.Printf("Watcher error: %v", err)
			}
		}
	}()

	if err := watcher.Add(filePath); err != nil {
		log.Fatalf("ERROR watching file: %v", err)
	}

	<-make(chan struct{}) // block forever
}

func main() {
	if filePath == "" {
		log.Fatal("AUDIT_FILE_PATH env var is required")
	}

	log.Printf("Watching Vault audit file: %s", filePath)
	watchFile()
}