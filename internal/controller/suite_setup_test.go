package controller

import (
	"context"
	"testing"

	testhelper "github.com/illumio/cloud-operator/internal/controller/testhelper"
	"github.com/stretchr/testify/suite"
	"k8s.io/client-go/kubernetes"
)

type ControllerTestSuite struct {
	suite.Suite
	ctx       context.Context
	clientset *kubernetes.Clientset
}

func TestGenerateTestSuite(t *testing.T) {
	suite.Run(t, new(ControllerTestSuite))
}

func (suite *ControllerTestSuite) SetupSuite() {
	suite.ctx = context.Background()
	err := testhelper.SetupTestCluster()
	if err != nil {
		panic("Failed to set up test cluster " + err.Error())
	}
}

func (suite *ControllerTestSuite) TearDownSuite() {
	err := testhelper.TearDownTestCluster()
	if err != nil {
		panic("Failed to delete test cluster " + err.Error())
	}
}
