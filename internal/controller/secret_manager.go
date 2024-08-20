// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"errors"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"

	"context"

	kuberneteserror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretManager keeps a logger for its own methods.
type SecretManager struct {
	Logger logr.Logger
}

// GetOnboardingCredentials returns credentials to onboard this cluster with CloudSecure.
func (sm *SecretManager) GetOnboardingCredentials(ctx context.Context, clientID string, clientSecret string) (Credentials, error) {
	clusterID, err := GetClusterID(ctx, sm.Logger)
	if err != nil {
		sm.Logger.Error(err, "Cannot get clusterID")
		return Credentials{}, err
	}
	return Credentials{ClusterID: clusterID, ClientID: clientID, ClientSecret: clientSecret}, nil
}

// ReadK8sSecret takes a secretName and reads the file.
func (sm *SecretManager) ReadCredentialsK8sSecrets(ctx context.Context, secretName string) (string, string, error) {
	// Create a new clientset
	clientset, err := NewClientSet()
	if err != nil {
		sm.Logger.Error(err, "Failed to create clientSet")
		return "", "", err
	}

	// Get the secrets
	secret, err := clientset.CoreV1().Secrets("illumio-cloud").Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		sm.Logger.Error(err, "Failed to get secret")
		return "", "", err
	}

	// Assuming your secret data has a "client_id" and "client_secret" key.
	clientID := string(secret.Data["client_id"])
	if clientID == "" {
		sm.Logger.Error(err, "Cannot get client_id")
		return "", "", errors.New("failed to get client_id out of secret")
	}
	clientSecret := string(secret.Data["client_secret"])
	if clientSecret == "" {
		sm.Logger.Error(err, "Cannot get client_secret")
		return "", "", errors.New("failed to get client_secret out of secret")
	}
	return clientID, clientSecret, nil
}

func (sm *SecretManager) DoesK8sSecretExist(ctx context.Context, secretName string) bool {
	clientset, err := NewClientSet()
	if err != nil {
		sm.Logger.Error(err, "Failed to create clientSet")
	}

	// Get the secret -> illumio-cloud will need to be configurable
	_, err = clientset.CoreV1().Secrets("illumio-cloud").Get(ctx, secretName, metav1.GetOptions{})
	return err == nil
}

// WriteK8sSecret takes a the PairingClusterResponse and writes it to a locally kept secret.
func (sm *SecretManager) WriteK8sSecret(ctx context.Context, keyData PairResponse, ClusterCreds string) error {
	clientset, err := NewClientSet()
	if err != nil {
		sm.Logger.Error(err, "Error creating clientset")
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

	// Create or Update the Secret.
	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if kuberneteserror.IsAlreadyExists(err) {
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			sm.Logger.Error(err, "Failed to update secret")
			return err
		}
	}
	if err != nil {
		sm.Logger.Error(err, "Error creating or updating secret")
		return err
	} else {
		sm.Logger.Info("Secret created or updated successfully")
	}
	return nil
}
