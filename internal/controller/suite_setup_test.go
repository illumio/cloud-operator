package controller

import (
	"context"
	"testing"
	"time"

	testhelper "github.com/illumio/cloud-operator/internal/controller/testhelper"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	var err error
	err = testhelper.SetupTestCluster()
	if err != nil {
		panic("Failed to set up test cluster " + err.Error())
	}
	// Create a new clientset
	suite.clientset, err = NewClientSet()
	if err != nil {
		panic("Failed to get client set " + err.Error())
	}

}

func (suite *ControllerTestSuite) TearDownSuite() {
	err := testhelper.TearDownTestCluster()
	if err != nil {
		panic("Failed to delete test cluster " + err.Error())
	}
}

func (suite *ControllerTestSuite) SetupTest() {
	// Delete the illumio-cloud namespace if it exists
	err := suite.clientset.CoreV1().Namespaces().Delete(context.TODO(), "illumio-cloud", metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		panic("Failed to delete illumio-cloud namespace " + err.Error())
	}
	time.Sleep(1 * time.Second)
}
