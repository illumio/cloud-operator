package controller

import (
	"context"
	"fmt"
	"testing"

	testhelper "github.com/illumio/cloud-operator/internal/controller/testhelper"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestGetOnboardingCredentials(t *testing.T) {
	ctx := context.Background()
	testhelper.SetupTestCluster()
	defer testhelper.TearDownTestCluster()

	t.Run("Success", func(t *testing.T) {
		// Initialize the logger
		zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
		logger := zapLogger.WithName("test")
		sm := &SecretManager{Logger: logger}

		clientID := "test-client-id"
		clientSecret := "test-client-secret"

		creds, err := sm.GetOnboardingCredentials(ctx, clientID, clientSecret)
		assert.NoError(t, err)
		clusterId, _ := GetClusterID(ctx, logger)
		assert.Equal(t, clusterId, creds.ClusterID)
		assert.Equal(t, clientID, creds.ClientID)
		assert.Equal(t, clientSecret, creds.ClientSecret)
	})

	t.Run("Empty ClientID", func(t *testing.T) {
		// Initialize the logger
		zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
		logger := zapLogger.WithName("test")
		sm := &SecretManager{Logger: logger}

		clientID := ""
		clientSecret := "test-client-secret"

		creds, err := sm.GetOnboardingCredentials(ctx, clientID, clientSecret)
		assert.Error(t, err)
		assert.Equal(t, Credentials{}, creds)
	})

	t.Run("Empty ClientSecret", func(t *testing.T) {
		// Initialize the logger
		zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
		logger := zapLogger.WithName("test")
		sm := &SecretManager{Logger: logger}

		clientID := "test-client-id"
		clientSecret := ""

		creds, err := sm.GetOnboardingCredentials(ctx, clientID, clientSecret)
		assert.Error(t, err)
		assert.Equal(t, Credentials{}, creds)
	})
}

func TestReadCredentialsK8sSecrets(t *testing.T) {
	ctx := context.Background()
	testhelper.SetupTestCluster()
	clientset, _ := NewClientSet()
	namespaceObj := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "illumio-cloud",
		},
	}
	clientset.CoreV1().Namespaces().Create(context.TODO(), namespaceObj, metav1.CreateOptions{})

	defer testhelper.TearDownTestCluster()
	t.Run("Success", func(t *testing.T) {
		zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
		logger := zapLogger.WithName("test")

		sm := &SecretManager{
			Logger: logger,
		}

		secretData := map[string][]byte{
			"client_id":     []byte("test-client-id"),
			"client_secret": []byte("test-client-secret"),
		}
		// Create the secret object
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-secret",
			},
			Data: secretData,
		}

		// Create the secret in the specified namespace
		clientset.CoreV1().Secrets("illumio-cloud").Create(context.TODO(), secret, metav1.CreateOptions{})
		clientID, clientSecret, err := sm.ReadCredentialsK8sSecrets(ctx, "test-secret")
		assert.NoError(t, err)
		assert.Equal(t, "test-client-id", clientID)
		assert.Equal(t, "test-client-secret", clientSecret)
	})

	t.Run("SecretNotFound", func(t *testing.T) {
		zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
		logger := zapLogger.WithName("test")

		sm := &SecretManager{
			Logger: logger,
		}

		clientID, clientSecret, err := sm.ReadCredentialsK8sSecrets(ctx, "non-existent-secret")
		assert.Error(t, err)
		assert.Empty(t, clientID)
		assert.Empty(t, clientSecret)
	})

	t.Run("ClientIDNotFound", func(t *testing.T) {
		zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
		logger := zapLogger.WithName("test")

		sm := &SecretManager{
			Logger: logger,
		}

		secretData := map[string][]byte{
			"client_secret": []byte("test-client-secret"),
		}
		// Create the secret object
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-secret-no-client-id",
			},
			Data: secretData,
		}
		expectedErrorMsg := "failed to get client_id out of secret"
		// Create the secret in the specified namespace
		clientset.CoreV1().Secrets("illumio-cloud").Create(context.TODO(), secret, metav1.CreateOptions{})
		clientID, clientSecret, err := sm.ReadCredentialsK8sSecrets(ctx, "test-secret-no-client-id")
		fmt.Print(err)
		fmt.Print("blah")
		assert.EqualErrorf(t, err, expectedErrorMsg, "Error should be: %v, got: %v", expectedErrorMsg, err)
		assert.Empty(t, clientID)
		assert.Empty(t, clientSecret)
	})

	t.Run("ClientSecretNotFound", func(t *testing.T) {
		zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
		logger := zapLogger.WithName("test")

		sm := &SecretManager{
			Logger: logger,
		}
		secretData := map[string][]byte{
			"client_id": []byte("test-client-id"),
		}
		// Create the secret object
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-secret-no-client-secret-id",
			},
			Data: secretData,
		}
		expectedErrorMsg := "failed to get client_secret out of secret"

		// Create the secret in the specified namespace
		clientset.CoreV1().Secrets("illumio-cloud").Create(context.TODO(), secret, metav1.CreateOptions{})
		clientID, clientSecret, err := sm.ReadCredentialsK8sSecrets(ctx, "test-secret-no-client-secret-id")
		assert.EqualErrorf(t, err, expectedErrorMsg, "Error should be: %v, got: %v", expectedErrorMsg, err)
		assert.Empty(t, clientID)
		assert.Empty(t, clientSecret)
	})
}

func TestWriteK8sSecret(t *testing.T) {
	ctx := context.Background()
	testhelper.SetupTestCluster()
	clientset, _ := NewClientSet()
	defer testhelper.TearDownTestCluster()

	t.Run("Failure", func(t *testing.T) {
		zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
		logger := zapLogger.WithName("test")
		expectedErrorMsg := "namespaces \"illumio-cloud\" not found"
		sm := &SecretManager{
			Logger: logger,
		}
		err := sm.WriteK8sSecret(ctx, PairResponse{ClusterClientId: "test-client-id", ClusterClientSecret: "test-client-secret"}, "test-secret")
		assert.EqualErrorf(t, err, expectedErrorMsg, "Error should be: %v, got: %v", expectedErrorMsg, err)
	})

	t.Run("Success", func(t *testing.T) {
		namespaceObj := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "illumio-cloud",
			},
		}
		clientset.CoreV1().Namespaces().Create(context.TODO(), namespaceObj, metav1.CreateOptions{})
		zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
		logger := zapLogger.WithName("test")

		sm := &SecretManager{
			Logger: logger,
		}
		err := sm.WriteK8sSecret(ctx, PairResponse{ClusterClientId: "test-client-id", ClusterClientSecret: "test-client-secret"}, "test-secret")
		assert.NoError(t, err)
	})

}
