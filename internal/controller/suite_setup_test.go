package controller

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/kubernetes"

	testhelper "github.com/illumio/cloud-operator/internal/controller/testhelper"
)

type ControllerTestSuite struct {
	suite.Suite
	ctx       context.Context
	clientset *kubernetes.Clientset
	logger    *zap.Logger
}

func TestGenerateTestSuite(t *testing.T) {
	suite.Run(t, new(ControllerTestSuite))
}

func (suite *ControllerTestSuite) SetupSuite() {
	suite.logger = newCustomLogger(suite.T())
	suite.ctx = context.Background()
	var err error
	err = testhelper.SetupTestCluster()
	if err != nil {
		suite.T().Fatal("Failed to set up test cluster " + err.Error())
	}
	// Create a new clientset
	suite.clientset, err = NewClientSet()
	if err != nil {
		suite.T().Fatal("Failed to get client set " + err.Error())
	}

}

func (suite *ControllerTestSuite) TearDownSuite() {
	err := testhelper.TearDownTestCluster()
	if err != nil {
		suite.T().Log("Failed to delete test cluster on first attempt: " + err.Error())

		// Retry deletion
		err = testhelper.TearDownTestCluster()
		if err != nil {
			suite.T().Fatal("Failed to delete test cluster after retry: " + err.Error())
		}
	}

	// Verify cluster deletion
	cmd := exec.Command("kind", "get", "clusters")
	output, err := cmd.Output()
	if err != nil {
		suite.T().Fatal("Failed to verify cluster deletion: " + err.Error())
	}

	if strings.Contains(string(output), "my-test-cluster") {
		suite.T().Fatal("Cluster 'my-test-cluster' still exists after deletion")
	}
}

func (suite *ControllerTestSuite) SetupTest() {
	suite.SynchronousDeleteNamespace("illumio-cloud", 10*time.Second)
}

// LogWriter is a writer that writes to a custom function
type LogWriter struct {
	logFunc func(string, ...interface{})
}

func (w *LogWriter) Write(p []byte) (n int, err error) {
	w.logFunc("%s", p)
	return len(p), nil
}

func (w *LogWriter) Sync() error {
	return nil
}

func newCustomLogger(t *testing.T) *zap.Logger {
	logWriter := &LogWriter{
		logFunc: t.Logf,
	}

	syncWriter := zapcore.AddSync(logWriter)
	encoderConfig := zap.NewDevelopmentEncoderConfig()
	encoder := zapcore.NewConsoleEncoder(encoderConfig)

	core := zapcore.NewCore(encoder, syncWriter, zap.DebugLevel)

	return zap.New(core)
}
