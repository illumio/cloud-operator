// Copyright 2024 Illumio, Inc. All Rights Reserved.
package controller

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// FalcoEvent represents the network information extracted from a Falco event.
type FalcoEvent struct {
	// SrcIP is the source IP address involved in the network event.
	SrcIP string `json:"srcip"`
	// DstIP is the destination IP address involved in the network event.
	DstIP string `json:"dstip"`
	// SrcPort is the source port number involved in the network event.
	SrcPort string `json:"srcport"`
	// DstPort is the destination port number involved in the network event.
	DstPort string `json:"dstport"`
	// Proto is the protocol used in the network event (e.g., TCP, UDP).
	Proto string `json:"proto"`
	// IpVersion is the version used in the network event (e.g. ipv4, ipv6).
	IpVersion string `json:"prototype"`
}

// parsePodNetworkInfo parses the input string to extract network information into a FalcoEvent struct.
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
			case "ipversion":
				info.IpVersion = value
			}
		}
	}
	return info, nil
}

// NewFalcoEventHandler creates a new HTTP handler function for processing Falco events.
func NewFalcoEventHandler(eventChan chan<- string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Output string `json:"output"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		eventChan <- body.Output
		w.WriteHeader(http.StatusOK)
	}
}

// filterIllumioTraffic filters out events related to Illumio network traffic.
func filterIllumioTraffic(body string) bool {
	return strings.Contains(body, "illumio_network_traffic")
}
