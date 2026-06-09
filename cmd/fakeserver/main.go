// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"flag"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/fakeserver"
)

func main() {
	// Parse flags
	proxyFlag := flag.Bool("proxy", false, "Start the proxy server")

	flag.Parse()

	logger, _ := zap.NewDevelopment()
	aud := "192.168.65.254:50051"
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

	fs := &fakeserver.FakeServer{
		Address:         "0.0.0.0:50051",
		HTTPAddress:     "0.0.0.0:50053",
		StopChan:        make(chan struct{}),
		Token:           signedToken,
		Logger:          logger,
		State:           &fakeserver.ServerState{},
		ConfigResponses: make(chan *pb.GetConfigurationUpdatesResponse, 10),
	}

	// Start the server
	if err := fs.Start(); err != nil {
		logger.Fatal("Failed to start server", zap.Error(err))
	}
	defer fs.Stop()

	logger.Info("FakeServer started")

	// Start the proxy server if the flag is set
	var proxyServer *fakeserver.ProxyServer
	if *proxyFlag {
		// Initialize the ProxyServer
		proxyServer = fakeserver.NewProxyServer("0.0.0.0:8888", logger)

		logger.Info("Starting ProxyServer")
		proxyServer.Start()

		defer func() {
			if err := proxyServer.Stop(); err != nil {
				logger.Error("Failed to stop ProxyServer", zap.Error(err))
			}
		}()
	}

	// Keep the compiler happy about proxyServer usage
	_ = proxyServer

	// Wait for server stop signal
	logger.Info("Server started")
	<-fs.StopChan
}
