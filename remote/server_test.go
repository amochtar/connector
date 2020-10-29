package remote

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/shipyard-run/connector/integrations"
	"github.com/shipyard-run/connector/protos/shipyard"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func createServer(t *testing.T, addr, name string) (*Server, *integrations.Mock) {
	certificate, err := tls.LoadX509KeyPair("../test/simple/certs/leaf.cert", "../test/simple/certs/leaf.key")
	require.NoError(t, err)

	// Create a certificate pool from the certificate authority
	certPool := x509.NewCertPool()
	ca, err := ioutil.ReadFile("../test/simple/certs/root.cert")
	require.NoError(t, err)

	ok := certPool.AppendCertsFromPEM(ca)
	require.True(t, ok)

	// create the mock
	mi := &integrations.Mock{}
	mi.On("Register", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mi.On("Deregister", mock.Anything).Return(nil)

	// start the gRPC server
	s := New(hclog.New(&hclog.LoggerOptions{Level: hclog.Trace, Name: name}), certPool, &certificate, mi)

	creds := credentials.NewTLS(&tls.Config{
		ClientAuth:   tls.RequireAndVerifyClientCert,
		Certificates: []tls.Certificate{certificate},
		ClientCAs:    certPool,
	})

	grpcServer := grpc.NewServer(grpc.Creds(creds))
	shipyard.RegisterRemoteConnectionServer(grpcServer, s)

	// create a listener for the server
	lis, err := net.Listen("tcp", addr)
	require.NoError(t, err)

	// start the server in the background
	go grpcServer.Serve(lis)

	t.Cleanup(func() {
		s.Shutdown()
		grpcServer.Stop()
		lis.Close()
	})

	return s, mi
}

func startLocalServer(t *testing.T) (string, *string) {
	bodyData := ""

	l := hclog.New(&hclog.LoggerOptions{Level: hclog.Trace, Name: "http_server"})

	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		l.Debug("Got request")

		data, _ := ioutil.ReadAll(r.Body)
		bodyData = string(data)
	}))

	t.Cleanup(func() {
		ts.Close()
	})

	return ts.Listener.Addr().String(), &bodyData
}

type serverStruct struct {
	Server      *Server
	Integration *integrations.Mock
}

var servers []serverStruct

func setupServers(t *testing.T) (string, *string) {
	// local server
	s1, m1 := createServer(t, ":1234", "server_local")
	s2, m2 := createServer(t, ":1235", "server_remote_1")
	s3, m3 := createServer(t, ":1236", "server_remote_2")

	servers = []serverStruct{
		{s1, m1},
		{s2, m2},
		{s3, m3},
	}

	// setup the local endpoint
	return startLocalServer(t)
}

func createClient(t *testing.T, addr string) shipyard.RemoteConnectionClient {
	certificate, err := tls.LoadX509KeyPair("../test/simple/certs/leaf.cert", "../test/simple/certs/leaf.key")
	require.NoError(t, err)

	// Create a certificate pool from the certificate authority
	certPool := x509.NewCertPool()
	ca, err := ioutil.ReadFile("../test/simple/certs/root.cert")
	require.NoError(t, err)

	ok := certPool.AppendCertsFromPEM(ca)
	require.True(t, ok)

	creds := credentials.NewTLS(&tls.Config{
		ServerName:   addr,
		Certificates: []tls.Certificate{certificate},
		RootCAs:      certPool,
	})

	// Create a connection with the TLS credentials
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)

	return shipyard.NewRemoteConnectionClient(conn)
}

func setupTests(t *testing.T) (shipyard.RemoteConnectionClient, string, *string) {
	tsAddr, tsData := setupServers(t)
	return createClient(t, "localhost:1234"), tsAddr, tsData
}

func TestExposeRemoteServiceCreatesLocalListener(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_REMOTE,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	time.Sleep(100 * time.Millisecond) // wait for setup

	// check the listener exists
	_, err = net.Dial("tcp", "localhost:19000")
	require.NoError(t, err)
}

func TestExposeRemoteServiceCallsIntegration(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_REMOTE,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	time.Sleep(100 * time.Millisecond) // wait for setup

	servers[0].Integration.AssertCalled(t, "Register", mock.Anything, "test-service", 19000, 19000)
}

func TestShutdownRemovesLocalListener(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_REMOTE,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	time.Sleep(100 * time.Millisecond) // wait for setup

	// check the listener exists
	_, err = net.Dial("tcp", "localhost:19000")
	require.NoError(t, err)

	// shutdown
	for _, s := range servers {
		s.Server.Shutdown()
	}

	_, err = net.Dial("tcp", "localhost:19000")
	require.Error(t, err)
}

func TestShutdownRemovesRemoteListener(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_LOCAL,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	time.Sleep(100 * time.Millisecond) // wait for setup

	// check the listener exists
	_, err = net.Dial("tcp", "localhost:19000")
	require.NoError(t, err)

	// shutdown
	for _, s := range servers {
		s.Server.Shutdown()
	}

	_, err = net.Dial("tcp", "localhost:19000")
	require.Error(t, err)
}

func TestExposeRemoteServiceCreatesLocalListener2(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_REMOTE,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	time.Sleep(100 * time.Millisecond) // wait for setup

	// check the listener exists
	_, err = net.Dial("tcp", "localhost:19000")
	require.NoError(t, err)
}

func TestExposeRemoteServiceUpdatesStatus(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_REMOTE,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	time.Sleep(100 * time.Millisecond) // wait for setup

	require.Eventually(t,
		func() bool {
			s, _ := c.ListServices(context.Background(), &shipyard.NullMessage{})
			if len(s.Services) > 0 {
				if s.Services[0].Status == shipyard.ServiceStatus_COMPLETE {
					return true
				}
			}

			return false
		},
		1*time.Second,
		50*time.Millisecond,
	)
}

func TestExposeRemoteDuplicateReturnsError(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test1",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_REMOTE,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	_, err = c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test2",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_REMOTE,
		},
	})

	require.Error(t, err)
}

func TestExposeLocalDuplicateReturnsError(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test1",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_LOCAL,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	_, err = c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test2",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_LOCAL,
		},
	})

	require.Error(t, err)
}
func TestExposeLocalDifferentServersReturnsOK(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test1",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_LOCAL,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	_, err = c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test2",
			RemoteConnectorAddr: "localhost:1236",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_LOCAL,
		},
	})

	require.NoError(t, err)
}

func TestExposeLocalDifferentConnectionsReturnsError(t *testing.T) {
	c, _, _ := setupTests(t)
	c2 := createClient(t, "localhost:1235")

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test1",
			RemoteConnectorAddr: "localhost:1236",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_LOCAL,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	require.Eventually(t,
		func() bool {
			s, _ := c.ListServices(context.Background(), &shipyard.NullMessage{})
			if len(s.Services) > 0 {
				if s.Services[0].Status == shipyard.ServiceStatus_COMPLETE {
					return true
				}
			}

			return false
		},
		1*time.Second,
		50*time.Millisecond,
	)

	resp, err = c2.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test2",
			RemoteConnectorAddr: "localhost:1236",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_LOCAL,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	// Both connections will return OK as there is no local validation failure
	// however the second connection will fail and the server will return a message
	// as the listener is in use

	require.Eventually(t,
		func() bool {
			s, _ := c2.ListServices(context.Background(), &shipyard.NullMessage{})
			if len(s.Services) > 0 {
				if s.Services[0].Status == shipyard.ServiceStatus_ERROR {
					return true
				}
			}

			return false
		},
		1*time.Second,
		50*time.Millisecond,
	)
}

func TestDestroyRemoteServiceRemovesLocalListener(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_REMOTE,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	require.Eventually(t,
		func() bool {
			s, _ := c.ListServices(context.Background(), &shipyard.NullMessage{})
			if len(s.Services) > 0 {
				if s.Services[0].Status == shipyard.ServiceStatus_COMPLETE {
					return true
				}
			}

			return false
		},
		1*time.Second,
		50*time.Millisecond,
	)

	// check the listener exists
	_, err = net.Dial("tcp", "localhost:19000")
	require.NoError(t, err)

	// remove the listener
	c.DestroyService(context.Background(), &shipyard.DestroyRequest{Id: resp.Id})
	time.Sleep(100 * time.Millisecond) // wait for setup

	// check the listener is not accessible
	_, err = net.Dial("tcp", "localhost:19000")
	require.Error(t, err)
}

func TestDestroyRemoteServiceRemovesLocalIntegration(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_REMOTE,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	require.Eventually(t,
		func() bool {
			s, _ := c.ListServices(context.Background(), &shipyard.NullMessage{})
			if len(s.Services) > 0 {
				if s.Services[0].Status == shipyard.ServiceStatus_COMPLETE {
					return true
				}
			}

			return false
		},
		1*time.Second,
		50*time.Millisecond,
	)

	// check the listener exists
	_, err = net.Dial("tcp", "localhost:19000")
	require.NoError(t, err)

	// remove the listener
	c.DestroyService(context.Background(), &shipyard.DestroyRequest{Id: resp.Id})
	time.Sleep(100 * time.Millisecond) // wait for setup

	servers[0].Integration.AssertCalled(t, "Deregister", "test-service")
}

func TestExposeLocalServiceCreatesRemoteListener(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19001,
			DestinationAddr:     "localhost:19000",
			Type:                shipyard.ServiceType_LOCAL,
		},
	})
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	// check the listener exists
	_, err = net.Dial("tcp", "localhost:19001")
	require.NoError(t, err)
}

func TestExposeLocalServiceCallsRemoteIntegration(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19000,
			DestinationAddr:     "localhost:19001",
			Type:                shipyard.ServiceType_LOCAL,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	time.Sleep(100 * time.Millisecond) // wait for setup

	servers[1].Integration.AssertCalled(t, "Register", mock.Anything, "test-service", 19000, 19000)
}

func TestDestroyLocalServiceRemovesRemoteListener(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19001,
			DestinationAddr:     "localhost:19000",
			Type:                shipyard.ServiceType_LOCAL,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	time.Sleep(100 * time.Millisecond)

	// check the listener exists
	_, err = net.Dial("tcp", "localhost:19001")
	require.NoError(t, err)

	// remove the listener
	c.DestroyService(context.Background(), &shipyard.DestroyRequest{Id: resp.Id})
	time.Sleep(100 * time.Millisecond) // wait for setup

	// check the listener is not accessible
	_, err = net.Dial("tcp", "localhost:19001")
	require.Error(t, err)
}

func TestDestroyLocalServiceRemovesRemoteIntegration(t *testing.T) {
	c, _, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			SourcePort:          19001,
			DestinationAddr:     "localhost:19000",
			Type:                shipyard.ServiceType_LOCAL,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	time.Sleep(100 * time.Millisecond)

	// check the listener exists
	_, err = net.Dial("tcp", "localhost:19001")
	require.NoError(t, err)

	// remove the listener
	c.DestroyService(context.Background(), &shipyard.DestroyRequest{Id: resp.Id})
	time.Sleep(100 * time.Millisecond) // wait for setup

	servers[1].Integration.AssertCalled(t, "Deregister", "test-service")
}

func TestExposeLocalServiceCreatesRemoteConnection(t *testing.T) {
	t.Skip()
}

func TestExposeRemoteServiceCreatesRemoteConnection(t *testing.T) {
	t.Skip()
}

func TestMessageToRemoteEndpointCallsLocalService(t *testing.T) {
	c, tsAddr, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			DestinationAddr:     tsAddr,
			SourcePort:          19001,
			Type:                shipyard.ServiceType_LOCAL,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	// wait while to ensure all setup
	time.Sleep(100 * time.Millisecond)

	// call the remote endpoint
	httpResp, err := http.DefaultClient.Get("http://localhost:19001")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, httpResp.StatusCode)

	httpResp, err = http.DefaultClient.Get("http://localhost:19001")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, httpResp.StatusCode)
}

func TestMessageToLocalEndpointCallsRemoteService(t *testing.T) {
	c, tsAddr, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			DestinationAddr:     tsAddr,
			SourcePort:          19001,
			Type:                shipyard.ServiceType_REMOTE,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	// wait while to ensure all setup
	time.Sleep(100 * time.Millisecond)

	// call the remote endpoint
	httpResp, err := http.DefaultClient.Get("http://localhost:19001")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, httpResp.StatusCode)

	httpResp, err = http.DefaultClient.Get("http://localhost:19001")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, httpResp.StatusCode)
}

func TestListServices(t *testing.T) {
	c, tsAddr, _ := setupTests(t)

	resp, err := c.ExposeService(context.Background(), &shipyard.ExposeRequest{
		Service: &shipyard.Service{
			Name:                "Test Service",
			RemoteConnectorAddr: "localhost:1235",
			DestinationAddr:     tsAddr,
			SourcePort:          19001,
			Type:                shipyard.ServiceType_REMOTE,
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.Id)

	// wait while to ensure all setup
	time.Sleep(100 * time.Millisecond)

	s, err := c.ListServices(context.Background(), &shipyard.NullMessage{})
	require.NoError(t, err)
	require.Len(t, s.Services, 1)
}
