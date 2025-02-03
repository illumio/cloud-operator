// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
)

var token = createSignedToken()

func createSignedToken() string {
	aud := "192.168.49.1:50051"
	token := "token1"
	// Example of generating a JWT with an "aud" claim
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": token,
		"aud": []string{aud},
		"exp": time.Now().Add(time.Hour * 72).Unix(),
	})
	// Just using "secret" for test signing
	mySigningKey := []byte("secret")

	// Sign and get the complete encoded token as a string
	// nosemgrep: jwt.hardcoded-jwt-key
	signedToken, err := jwtToken.SignedString(mySigningKey)
	if err != nil {
		logger.Error("Token could not be signed with fake secret key")
	}
	return signedToken

}

type MyTestSuite struct {
	suite.Suite
	logger *zap.SugaredLogger
}

func (suite *MyTestSuite) SetupSuite() {
	suite.logger = zap.NewNop().Sugar()
	suite.runHelmCommand()
}

func (suite *MyTestSuite) runHelmCommand() {
	settings := cli.New()
	actionConfig := new(action.Configuration)
	err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), os.Getenv("HELM_DRIVER"), suite.logger.Infof)
	if err != nil {
		suite.T().Fatal("Failed to initialize Helm action configuration:", err)
	}

	client := action.NewInstall(actionConfig)
	client.Namespace = "illumio-cloud"
	client.ReleaseName = "illumio"

	values := map[string]interface{}{
		"image": map[string]interface{}{
			"repository": fmt.Sprintf("ghcr.io/%s", os.Getenv("GITHUB_REPOSITORY")),
			"tag":        os.Getenv("GITHUB_REF_NAME"),
			"pullPolicy": "Always",
		},
		"onboardingSecret": map[string]interface{}{
			"clientId":     os.Getenv("ONBOARDING_CLIENT_ID"),
			"clientSecret": os.Getenv("ONBOARDING_CLIENT_SECRET"),
		},
		"env": map[string]interface{}{
			"onboardingEndpoint": os.Getenv("ONBOARDING_ENDPOINT"),
			"tokenEndpoint":      os.Getenv("TOKEN_ENDPOINT"),
			"tlsSkipVerify":      os.Getenv("TLS_SKIP_VERIFY"),
		},
		"falco": map[string]interface{}{
			"enabled": false,
		},
	}
	// Load the chart file
	chartPath := "cloud-operator-1.0.0.tgz"
	chart, err := loader.Load(chartPath)
	if err != nil {
		suite.T().Fatalf("failed to load chart: %v", err)
	}
	_, err = client.Run(chart, values)
	if err != nil {
		suite.T().Fatal("Failed to run Helm upgrade/install:", err)
	}
}

func TestMySuite(t *testing.T) {
	suite.Run(t, new(MyTestSuite))
}

// TestFakeServerConnectionSuccesfulAndRetry tests a client connecting to the gRPC server
func (suite *MyTestSuite) TestFakeServerConnectionSuccesfulAndRetry(t *testing.T) {
	logger := zap.NewNop()
	// Setup: Start the FakeServer
	fakeServer := &FakeServer{
		address:     "0.0.0.0:50051",
		httpAddress: "0.0.0.0:50053",
		token:       token,
		logger:      logger, // Use a no-op logger for testing
		state:       &ServerState{ConnectionSuccessful: false},
	}

	// Start the server
	err := fakeServer.start()
	assert.NoError(suite.T(), err, "Failed to start the FakeServer")

	// Cleanup: Stop the server after the test
	defer fakeServer.stop()

	// Wait for the state to change, Wanting to see client succesfully connect to fakeserver
	timeout := time.After(120 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
mainloop:
	for {
		select {
		case <-timeout:
			// Test failure if the state hasn't changed in time
			suite.T().Fatal("Operator never connected in alloted time.")
			return
		case <-ticker.C:
			// time.Sleep(25 * time.Second)
			// Check if the log entry has been recorded
			stateChanged := fakeServer.state.ConnectionSuccessful

			// Check if the log entry we sent is in the received logs
			if stateChanged {
				suite.T().Log("Connection Succes is true. Test passed.")
				assert.Equal(suite.T(), stateChanged, true)
				break mainloop
			}
		}
	}
	fakeServer.stop()

	fakeServer.state.ConnectionSuccessful = false
	// Start the server
	err = fakeServer.start()
	assert.NoError(suite.T(), err, "Failed to start the FakeServer")
	// Cleanup: Stop the server after the test
	defer fakeServer.stop()

	for {
		select {
		case <-timeout:
			// Test failure if the state hasn't changed in time
			suite.T().Fatal("Operator never connected in alloted time.")
			return
		case <-ticker.C:
			// Check if the log entry has been recorded
			stateChanged := fakeServer.state.ConnectionSuccessful

			// Check if the log entry we sent is in the received logs
			if stateChanged {
				suite.T().Log("Connection Succes is true. Test passed.")
				assert.Equal(suite.T(), stateChanged, true)
				return
			}
		}
	}
}

func (suite *MyTestSuite) TestFailureDuringIntialCommit(t *testing.T) {
	logger := zap.NewNop()
	// Setup: Start the FakeServer
	fakeServer := &FakeServer{
		address:     "0.0.0.0:50051",
		httpAddress: "0.0.0.0:50053",
		token:       token,
		logger:      logger, // Use a no-op logger for testing
		state:       &ServerState{ConnectionSuccessful: false, BadIntialCommit: true},
	}

	// Start the server
	err := fakeServer.start()
	assert.NoError(suite.T(), err, "Failed to start the FakeServer")

	// Cleanup: Stop the server after the test
	defer fakeServer.stop()

	// Wait for the state to change, Wanting to see client succesfully connect to fakeserver
	timeout := time.After(120 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)

mainloop:
	for {
		select {
		case <-timeout:
			// Test failure if the state hasn't changed in time
			suite.T().Fatal("Operator never connected in alloted time.")
			return
		case <-ticker.C:
			// Check if the log entry has been recorded
			stateChanged := fakeServer.state.ConnectionSuccessful

			// Check if the log entry we sent is in the received logs
			if stateChanged {
				suite.T().Log("Connection Succes is true. Test passed.")
				assert.Equal(suite.T(), stateChanged, true)
				break mainloop
			}
		}
	}
	fakeServer.stop()
}
