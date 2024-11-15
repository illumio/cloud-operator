package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

type FalcoEvent struct {
	SrcIP   string `json:"srcip"`
	DstIP   string `json:"dstip"`
	SrcPort string `json:"srcport"`
	DstPort string `json:"dstport"`
	Proto   string `json:"proto"`
}

func parsePodNetworkInfo(input string) (FalcoEvent, error) {
	var info FalcoEvent

	// Regular expression to extract the key-value pairs from the input string
	re := regexp.MustCompile(`\b(\w+)=([^\s)]+)`)
	matches := re.FindAllStringSubmatch(input, -1)

	for _, match := range matches {
		if len(match) == 3 {
			key, value := match[1], match[2]
			switch key {
			case "srcip":
				info.SrcIP = value
			case "dstip":
				info.DstIP = value
			case "srcport":
				info.SrcPort = value
			case "dstport":
				info.DstPort = value
			case "proto":
				info.Proto = value
			}
		}
	}
	return info, nil
}

func NewFalcoEventHandler(eventChan chan<- FalcoEvent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Output string `json:"output"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if filterIllumioTraffic(body.Output) {
			// Extract the relevant part of the output string
			re := regexp.MustCompile(`\((.*?)\)`)
			match := re.FindStringSubmatch(body.Output)
			if len(match) < 2 {
				http.Error(w, "Invalid input format", http.StatusBadRequest)
				return
			}

			podInfo, err := parsePodNetworkInfo(match[1])
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			eventChan <- podInfo

			// Process the pod network info data
			fmt.Printf("Received pod network info: %+v\n", podInfo)

		}
		w.WriteHeader(http.StatusOK)
	}
}

func filterIllumioTraffic(body string) bool {
	return !strings.Contains(body, "illumio_network_traffic")
}
