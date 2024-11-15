package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"

	"github.com/stretchr/testify/assert"
)

func (suite *ControllerTestSuite) TestParsePodNetworkInfo() {
	tests := map[string]struct {
		input       string
		expected    FalcoEvent
		expectedErr string
	}{
		"valid input with TCP": {
			input: "srcip=192.168.0.1 dstip=192.168.0.2 srcport=80 dstport=8080 proto=TCP",
			expected: FalcoEvent{
				SrcIP:   "192.168.0.1",
				DstIP:   "192.168.0.2",
				SrcPort: "80",
				DstPort: "8080",
				Proto:   "TCP",
			},
			expectedErr: "",
		},
		"valid input with UDP": {
			input: "srcip=10.0.0.1 dstip=10.0.0.2 srcport=443 dstport=8443 proto=UDP",
			expected: FalcoEvent{
				SrcIP:   "10.0.0.1",
				DstIP:   "10.0.0.2",
				SrcPort: "443",
				DstPort: "8443",
				Proto:   "UDP",
			},
			expectedErr: "",
		},
		"missing values for some keys": {
			input: "srcip=192.168.1.1 dstip=192.168.1.2 proto=TCP",
			expected: FalcoEvent{
				SrcIP: "192.168.1.1",
				DstIP: "192.168.1.2",
				Proto: "TCP",
			},
			expectedErr: "",
		},
		"invalid input format": {
			input:       "blah=invalid evan",
			expected:    FalcoEvent{},
			expectedErr: "",
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result, err := parsePodNetworkInfo(tt.input)
			if tt.expectedErr != "" {
				assert.EqualError(suite.T(), err, tt.expectedErr)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tt.expected, result)
			}
		})
	}
}

// TestFilterIllumioTraffic tests the filterIllumioTraffic function
func (suite *ControllerTestSuite) TestFilterIllumioTraffic() {
	tests := map[string]struct {
		input    string
		expected bool
	}{
		"contains Illumio traffic": {
			input:    "some text with illumio_network_traffic inside",
			expected: true,
		},
		"does not contain Illumio traffic": {
			input:    "some regular log message",
			expected: false,
		},
		"empty string": {
			input:    "",
			expected: false,
		},
		"Illumio traffic at start": {
			input:    "illumio_network_traffic some other text",
			expected: true,
		},
		"Illumio traffic at end": {
			input:    "some other text illumio_network_traffic",
			expected: true,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result := filterIllumioTraffic(tt.input)
			assert.Equal(suite.T(), tt.expected, result)
		})
	}
}

func (suite *ControllerTestSuite) TestNewFalcoEventHandler() {
	handler := NewFalcoEventHandler(suite.eventChan)

	tests := map[string]struct {
		input          string
		expectedStatus int
		expectedEvent  *FalcoEvent
		expectedErrMsg string
	}{
		"valid Falco event": {
			input:          `{"output": "some text (srcip=192.168.0.1 dstip=192.168.0.2 srcport=80 dstport=8080 proto=TCP) some other text"}`,
			expectedStatus: http.StatusOK,
		},
		"event filtered out": {
			input:          `{"output": "some text with nothing inside"}`,
			expectedStatus: http.StatusOK,
		},
		"invalid JSON": {
			input:          `{"output": "some text`,
			expectedStatus: http.StatusBadRequest,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tt.input))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			// Check the status code is what we expect.
			assert.Equal(suite.T(), tt.expectedStatus, rr.Code)

		})
	}
}
