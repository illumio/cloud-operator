package fakeserver

import (
	"context"
	"testing"
	"time"

	"github.com/illumio/cloud-operator/internal/controller/testhelper"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
)

type ControllerTestSuite struct {
	suite.Suite
	logger *zap.Logger
}

func TestGenerateTestSuite(t *testing.T) {
	suite.Run(t, new(ControllerTestSuite))
}

func (suite *ControllerTestSuite) SetupSuite() {
	suite.logger, _ = zap.NewDevelopment()
}

func (suite *ControllerTestSuite) TearDownSuite() {

}

func (suite *ControllerTestSuite) SetupTest() {

}
