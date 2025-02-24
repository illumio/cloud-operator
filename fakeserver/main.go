package main

import (
	"time"

	"github.com/golang-jwt/jwt"
	"go.uber.org/zap"
)

func main() {
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
	mySigningKey := []byte("token1")

	// Sign and get the complete encoded token as a string
	// nosemgrep: jwt.hardcoded-jwt-key
	signedToken, err := jwtToken.SignedString(mySigningKey)
	if err != nil {
		logger.Error("Token could not be signed with fake secret key")
	}
	fs := FakeServer{
		address:     "0.0.0.0:50051",
		httpAddress: "0.0.0.0:50053",
		stopChan:    make(chan struct{}),
		token:       signedToken,
		logger:      logger,
		state:       &ServerState{},
	}

	// Start the server
	if err := fs.start(); err != nil {
		logger.Fatal("Failed to start server", zap.Error(err))
	}
	defer fs.stop()

	// Wait indefinitely for server stop signal
	logger.Info("Server started")
	<-fs.stopChan
}
