package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/shipyard-run/connector/http"
	"github.com/shipyard-run/connector/protos/shipyard"
	"github.com/shipyard-run/connector/remote"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the connector",
	Long:  `Runs the connector with the given options`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Do Stuff Here

		lo := hclog.LoggerOptions{}
		lo.Level = hclog.LevelFromString(logLevel)
		l := hclog.New(&lo)

		grpcServer := grpc.NewServer()
		s := remote.New(l.Named("grpc_server"), nil, nil)

		// do we need to set up the server to use TLS?
		if pathCertServer != "" && pathKeyServer != "" && pathCertRoot != "" {
			certificate, err := tls.LoadX509KeyPair(pathCertServer, pathKeyServer)
			if err != nil {
				return fmt.Errorf("could not load server key pair: %s", err)
			}

			// Create a certificate pool from the certificate authority
			certPool := x509.NewCertPool()
			ca, err := ioutil.ReadFile(pathCertRoot)
			if err != nil {
				return fmt.Errorf("could not read ca certificate: %s", err)
			}

			// Append the client certificates from the CA
			if ok := certPool.AppendCertsFromPEM(ca); !ok {
				return errors.New("failed to append client certs")
			}

			creds := credentials.NewTLS(&tls.Config{
				ClientAuth:   tls.RequireAndVerifyClientCert,
				Certificates: []tls.Certificate{certificate},
				ClientCAs:    certPool,
			})

			grpcServer = grpc.NewServer(grpc.Creds(creds))
			s = remote.New(l.Named("grpc_server"), certPool, &certificate)
		}

		shipyard.RegisterRemoteConnectionServer(grpcServer, s)

		// create a listener for the server
		l.Info("Starting gRPC server", "bind_addr", grpcBindAddr)
		lis, err := net.Listen("tcp", grpcBindAddr)
		if err != nil {
			l.Error("Unable to list on address", "bind_addr", grpcBindAddr)
			os.Exit(1)
		}

		// start the gRPC server
		go grpcServer.Serve(lis)

		// start the http server in the background
		l.Info("Starting HTTP server", "bind_addr", httpBindAddr)
		httpS := http.NewLocalServer(pathCertRoot, pathCertServer, pathKeyServer, grpcBindAddr, httpBindAddr, l)
		err = httpS.Serve()
		if err != nil {
			l.Error("Unable to start HTTP server", "error", err)
			os.Exit(1)
		}

		for {
			time.Sleep(100 * time.Millisecond)
		}

		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		signal.Notify(c, os.Kill)

		// Block until a signal is received.
		sig := <-c
		log.Println("Got signal:", sig)

		s.Shutdown()

		return nil
	},
}

var grpcBindAddr string
var httpBindAddr string
var pathCertRoot string
var pathCertServer string
var pathKeyServer string
var logLevel string

func init() {
	runCmd.Flags().StringVarP(&grpcBindAddr, "grpc-bind", "", ":9090", "Bind address for the gRPC API")
	runCmd.Flags().StringVarP(&httpBindAddr, "http-bind", "", ":9091", "Bind address for the HTTP API")
	runCmd.Flags().StringVarP(&pathCertRoot, "root-cert-path", "", "", "Path for the PEM encoded TLS root certificate")
	runCmd.Flags().StringVarP(&pathCertServer, "server-cert-path", "", "", "Path for the servers PEM encoded TLS certificate")
	runCmd.Flags().StringVarP(&pathKeyServer, "server-key-path", "", "", "Path for the servers PEM encoded Private Key")
	runCmd.Flags().StringVarP(&logLevel, "log-level", "", "info", "Log output level [debug, trace, info]")
}
