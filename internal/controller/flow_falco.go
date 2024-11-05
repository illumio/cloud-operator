package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/falcosecurity/client-go/pkg/api/outputs"
	"github.com/falcosecurity/client-go/pkg/client"
	"github.com/gogo/protobuf/jsonpb"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type falcoFlowCollector struct {
	logger *zap.SugaredLogger
	client *client.Client
}

const (
	falcoServiceName string = "falco"
)

var (
	ErrFalcoNotFound        = errors.New("falco not found; disabling falco flow collection")
	ErrNoFalcoPortAvailible = errors.New("falco has no ports; disabling falco flow collection")
)

func discoverFalcoAddress(ctx context.Context, falcoNamespace string, clientset kubernetes.Interface) (string, int32, error) {
	service, err := clientset.CoreV1().Services(falcoNamespace).Get(ctx, falcoServiceName, metav1.GetOptions{})
	if err != nil {
		return "", 0, ErrFalcoNotFound
	}

	if len(service.Spec.Ports) == 0 {
		return "", 0, ErrNoFalcoPortAvailible
	}

	return service.Spec.ClusterIP, service.Spec.Ports[0].Port, nil
}

func newFalcoFlowCollector(ctx context.Context, logger *zap.SugaredLogger, falcoNamespace string) (*falcoFlowCollector, error) {
	config, err := NewClientSet()
	if err != nil {
		return nil, fmt.Errorf("failed to create new client set: %w", err)
	}
	hostname, port, err := discoverFalcoAddress(ctx, falcoNamespace, config)
	// Set up a connection to the server.
	c, err := client.NewForConfig(context.Background(), &client.Config{
		Hostname:   hostname,
		Port:       uint16(port),
		CertFile:   "/etc/falco/certs/client.crt",
		KeyFile:    "/etc/falco/certs/client.key",
		CARootFile: "/etc/falco/certs/ca.crt",
	})
	if err != nil {
		log.Fatalf("unable to connect: %v", err)
	}
	return &falcoFlowCollector{logger: logger, client: c}, nil
}

func (fc *falcoFlowCollector) readFalcoEvents(ctx context.Context, sm streamManager) error {

	outputsClient, err := fc.client.Outputs()
	if err != nil {
		log.Fatalf("unable to obtain an output client: %v", err)
	}

	fcs, err := outputsClient.Get(ctx, &outputs.Request{})
	if err != nil {
		log.Fatalf("could not subscribe: %v", err)
	}
	defer func() {
		err = fcs.CloseSend()
		if err != nil {
			fc.logger.Errorw("Error closing serviceClient stream", "error", err)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		res, err := fcs.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("error closing stream after EOF: %v", err)
		}
		out, err := (&jsonpb.Marshaler{}).MarshalToString(res)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(out)
	}
	return nil
}
