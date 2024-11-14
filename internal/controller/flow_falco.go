package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/falcosecurity/client-go/pkg/client"
	"go.uber.org/zap"
)

type falcoFlowCollector struct {
	logger *zap.SugaredLogger
	client *client.Client
}

type FalcoEvent struct {
	message string
}

func NewFalcoEventHandler(w http.ResponseWriter, r *http.Request, ch chan FalcoEvent) {


	// In this function we need to filter and decide whether or not this is an event we want to keep.
	// If this is an event we want to keep than we must push this event to the falcoEvent Chan, when pushed to the cha
	// We will have antoher worker start pushing these events over a grpc stream.





	// Print the request method, URL, and headers
	fmt.Printf("Method: %s\n", r.Method)
	fmt.Printf("URL: %s\n", r.URL.String())
	fmt.Printf("Headers: %v\n", r.Header)

	// Optionally, print the request body if it's not nil
	if r.Body != nil {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err == nil {
			fmt.Printf("Body: %s\n", string(body))
		}
	}

	// Respond to the client
	fmt.Fprintf(w, "Request received")
}

// FilterEvent filters a single event based on the message field
func FilterEvent(eventStr string) (bool, error) {
	var event FalcoEvent
	if err := json.Unmarshal([]byte(eventStr), &event); err != nil {
		return false, err
	}
	return event.message == "Illumio traffic watch", nil
}
