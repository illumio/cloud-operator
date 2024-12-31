package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"
)

func main() {
	fs := FakeServer{
		address:  "127.0.0.1:50051",
		stopChan: make(chan struct{}),
		token:    "fake_test_token",
		logger:   &zap.Logger{},
	}

	// Start the server
	if err := fs.start(); err != nil {
		fmt.Println("Failed to start server:", err)
		os.Exit(1)
	}
	defer fs.stop()

	// Wait indefinitely for server stop signal
	<-fs.stopChan
}
