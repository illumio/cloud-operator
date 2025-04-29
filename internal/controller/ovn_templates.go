package controller

// IPFIXHeader represents the header of an IPFIX message.
type IPFIXHeader struct {
	Version        uint16 // IPFIX version number
	Length         uint16 // Length of the message
	ExportTime     uint32 // Time when the message was exported
	SequenceNumber uint32 // Sequence number of the message
	ObservationID  uint32 // Observation domain ID
}

// IPFIXFlowRecord represents a flow record in an IPFIX message.
type IPFIXFlowRecord struct {
	SourceIP        string // Source IP address
	DestinationIP   string // Destination IP address
	SourcePort      uint16 // Source port
	DestinationPort uint16 // Destination port
	Protocol        uint8  // Protocol (e.g., TCP, UDP)
	Timestamp       uint32 // Timestamp of the flow
}

// IPFIXMessage represents an IPFIX message containing a header and flow records.
type IPFIXMessage struct {
	Header      IPFIXHeader       // IPFIX message header
	FlowRecords []IPFIXFlowRecord // Flow records
}

// Template represents a predefined structure for parsing flow records.
type Template struct {
	ID     uint16         // Template ID
	Fields map[string]int // Field names and their byte offsets
}

// Predefined templates for processing TCP/UDP flows.
var ipfixTemplates = map[uint16]Template{}

// Protocol enums for template generation
const (
	IPFIX_PROTO_L2_ETH = iota
	IPFIX_PROTO_L2_VLAN
	NUM_IPFIX_PROTO_L2
)

const (
	IPFIX_PROTO_L3_UNKNOWN = iota
	IPFIX_PROTO_L3_IPV4
	IPFIX_PROTO_L3_IPV6
	NUM_IPFIX_PROTO_L3
)

const (
	IPFIX_PROTO_L4_UNKNOWN = iota
	IPFIX_PROTO_L4_TCP
	IPFIX_PROTO_L4_UDP
	IPFIX_PROTO_L4_SCTP
	IPFIX_PROTO_L4_ICMP
	NUM_IPFIX_PROTO_L4
)

const (
	IPFIX_PROTO_NOT_TUNNELED = iota
	IPFIX_PROTO_TUNNELED
	NUM_IPFIX_PROTO_TUNNEL
)

const IPFIX_TEMPLATE_ID_MIN = 256

// Generate all possible templates based on protocol combinations
func init() {
	ipfixTemplates = make(map[uint16]Template)
	templateID := IPFIX_TEMPLATE_ID_MIN

	for l2 := 0; l2 < NUM_IPFIX_PROTO_L2; l2++ {
		for l3 := 0; l3 < NUM_IPFIX_PROTO_L3; l3++ {
			for l4 := 0; l4 < NUM_IPFIX_PROTO_L4; l4++ {
				for tunnel := 0; tunnel < NUM_IPFIX_PROTO_TUNNEL; tunnel++ {
					ipfixTemplates[uint16(templateID)] = Template{
						ID: uint16(templateID),
						Fields: map[string]int{
							"L2":     l2,
							"L3":     l3,
							"L4":     l4,
							"Tunnel": tunnel,
						},
					}
					templateID++
				}
			}
		}
	}
}
