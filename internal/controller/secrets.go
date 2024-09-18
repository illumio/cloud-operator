// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"errors"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"

	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Secrets keeps a logger for its own methods.
type Secrets struct {
	Logger *zap.SugaredLogger
}

// GetOnboardingCredentials returns credentials to onboard this cluster with CloudSecure.
func (secrets *Secrets) GetOnboardingCredentials(ctx context.Context, clientID string, clientSecret string) (Credentials, error) {
	if clientID == "" || clientSecret == "" {
		return Credentials{}, errors.New("incomplete credentials found")
	}
	return Credentials{ClientID: clientID, ClientSecret: clientSecret}, nil
}

// ReadK8sSecret takes a secretName and reads the file.
func (secrets *Secrets) ReadCredentialsK8sSecrets(ctx context.Context, secretName string) (string, string, error) {
	// Create a new clientset
	clientset, err := NewClientSet()
	if err != nil {
		secrets.Logger.Errorw("Failed to create clientSet", "error", err)
		return "", "", err
	}

	// Get the secret
	secret, err := clientset.CoreV1().Secrets("illumio-cloud").Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		secrets.Logger.Errorw("Failed to get secret", "error", err)
		return "", "", err
	}

	// Assuming your secret data has a "client_id" and "client_secret" key.
	clientID := string(secret.Data["client_id"])
	if clientID == "" {
		secrets.Logger.Errorw("Cannot get client_id", "error", err)
		return "", "", errors.New("failed to get client_id from secret")
	}
	clientSecret := string(secret.Data["client_secret"])
	if clientSecret == "" {
		secrets.Logger.Errorw("Cannot get client_secret", "error", err)
		return "", "", errors.New("failed to get client_secret from secret")
	}
	return clientID, clientSecret, nil
}

func (secrets *Secrets) DoesK8sSecretExist(ctx context.Context, secretName string) bool {
	clientset, err := NewClientSet()
	if err != nil {
		secrets.Logger.Errorw("Failed to create clientSet", "error", err)
	}

	// Get the secret -> illumio-cloud will need to be configurable
	_, err = clientset.CoreV1().Secrets("illumio-cloud").Get(ctx, secretName, metav1.GetOptions{})
	return err == nil
}

// WriteK8sSecret takes an OnboardResponse and writes it to a locally kept secret.
func (secrets *Secrets) WriteK8sSecret(ctx context.Context, keyData OnboardResponse, ClusterCreds string) error {
	clientset, err := NewClientSet()
	if err != nil {
		secrets.Logger.Errorw("Failed to create clientSet", "error", err)
		return err
	}
	secretData := map[string]string{
		"client_id":     keyData.ClusterClientId,
		"client_secret": keyData.ClusterClientSecret,
	}
	namespace := "illumio-cloud" // Will be made configurable.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ClusterCreds,
			Namespace: namespace,
		},
		StringData: secretData,
	}

	_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		secrets.Logger.Errorw("Failed to update secret", "error", err)
		return err
	}
	return nil
}
