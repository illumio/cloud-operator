package controller

import (
	"context"
	"os"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func (suite *ControllerTestSuite) TestFetchResources() {
	// Setup logger
	encoderConfig := zap.NewDevelopmentEncoderConfig()
	encoder := zapcore.NewJSONEncoder(encoderConfig)
	consoleSyncer := zapcore.AddSync(os.Stdout)
	core := zapcore.NewTee(
		zapcore.NewCore(encoder, consoleSyncer, zapcore.InfoLevel),
	)
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()
	logger = logger.With(zap.String("name", "test"))
	// Create dynamic client
	clusterConfig, _ := rest.InClusterConfig()
	dynamicClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		suite.T().Fatal("Error creating dynamic client", "error", err)
	}

	resourceManager := &ResourceManager{
		dynamicClient: dynamicClient,
		logger:        logger,
	}
	tests := map[string]struct {
		resource  schema.GroupVersionResource
		namespace string
		expectErr bool
	}{
		"valid namespace": {
			resource:  schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			namespace: "default",
			expectErr: false,
		},
		"invalid namespace": {
			resource:  schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			namespace: "nonexistent-namespace",
			expectErr: true,
		},
		"empty result": {
			resource:  schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nonexistent-resource"},
			namespace: "default",
			expectErr: false,
		},
	}

	for name, tc := range tests {
		suite.Run(name, func() {
			ctx := context.Background()
			resources, err := resourceManager.FetchResources(ctx, tc.resource, tc.namespace)
			if tc.expectErr {
				assert.Error(suite.T(), err)
			} else {
				assert.NoError(suite.T(), err)
				assert.NotNil(suite.T(), resources)
				if name == "empty result" {
					assert.Empty(suite.T(), resources.Items)
				}
			}
		})
	}
}
