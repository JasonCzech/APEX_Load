package generatorrpc

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Transport security for the controller<->generator gRPC channel. Without it
// the channel is plaintext and unauthenticated, leaving NetworkPolicy as the
// only barrier — which silently no-ops on CNIs that do not enforce policies.
//
// The env contract is symmetric: each process declares its own identity and
// the CA it trusts for the peer.
//
//	APEXLOAD_GRPC_TLS_CERT / APEXLOAD_GRPC_TLS_KEY
//	    This process's certificate and key (server cert on the generator,
//	    client cert on the controller).
//	APEXLOAD_GRPC_TLS_CA
//	    CA bundle used to verify the peer. On the generator, setting it
//	    additionally requires and verifies client certificates (mTLS). On the
//	    controller, setting it enables TLS and verifies the generator cert.
//	APEXLOAD_GRPC_TLS_SERVER_NAME
//	    Controller-side verification/SNI override. Required when generator
//	    targets are pod IPs (dns:// discovery) and certificates carry a DNS
//	    SAN such as apexload-generator.apexload.svc.cluster.local.
//	APEXLOAD_GRPC_AUTH_TOKEN
//	    Shared bearer token. The generator rejects every RPC that does not
//	    carry it; the controller attaches it to every RPC. Defense in depth
//	    independent of the network layer — set it even with mTLS.
//
// Certificate and CA files are re-read on every dial / server start so that
// Kubernetes secret rotation is picked up without code-level cert caching.

const (
	grpcTLSCertEnvVar       = "APEXLOAD_GRPC_TLS_CERT"
	grpcTLSKeyEnvVar        = "APEXLOAD_GRPC_TLS_KEY"
	grpcTLSCAEnvVar         = "APEXLOAD_GRPC_TLS_CA"
	grpcTLSServerNameEnvVar = "APEXLOAD_GRPC_TLS_SERVER_NAME"
	grpcAuthTokenEnvVar     = "APEXLOAD_GRPC_AUTH_TOKEN"

	grpcAuthorizationHeader = "authorization"
	grpcBearerPrefix        = "Bearer "
)

type transportEnv struct {
	CertFile   string
	KeyFile    string
	CAFile     string
	ServerName string
	Token      string
}

func loadTransportEnv() transportEnv {
	return transportEnv{
		CertFile:   strings.TrimSpace(os.Getenv(grpcTLSCertEnvVar)),
		KeyFile:    strings.TrimSpace(os.Getenv(grpcTLSKeyEnvVar)),
		CAFile:     strings.TrimSpace(os.Getenv(grpcTLSCAEnvVar)),
		ServerName: strings.TrimSpace(os.Getenv(grpcTLSServerNameEnvVar)),
		Token:      strings.TrimSpace(os.Getenv(grpcAuthTokenEnvVar)),
	}
}

// TransportSecurity describes the effective channel posture for startup
// logging and production-posture warnings.
type TransportSecurity struct {
	TLS       bool
	MutualTLS bool
	Token     bool
}

func (t TransportSecurity) Insecure() bool { return !t.TLS && !t.Token }

// productionEnv is the canonical APEXLOAD_ENV value that arms the fail-closed posture.
const productionEnv = "production"

// MustRefuseInsecure reports whether a process running in environment env must
// refuse to start given this transport posture. Production requires an
// encrypted channel and authenticated clients: either mTLS or TLS plus the
// bearer token. In particular, a token is never accepted over plaintext and a
// server certificate alone is not client authentication.
func (t TransportSecurity) MustRefuseInsecure(env string) bool {
	if !isProductionEnvironment(env) {
		return false
	}
	return !t.TLS || (!t.MutualTLS && !t.Token)
}

func isProductionEnvironment(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case productionEnv, "prod":
		return true
	default:
		return false
	}
}

func (t TransportSecurity) String() string {
	parts := make([]string, 0, 3)
	if t.MutualTLS {
		parts = append(parts, "mtls")
	} else if t.TLS {
		parts = append(parts, "tls")
	} else {
		parts = append(parts, "plaintext")
	}
	if t.Token {
		parts = append(parts, "token-auth")
	} else {
		parts = append(parts, "no-token")
	}
	return strings.Join(parts, "+")
}

// TransportSecurityFromEnv reports the posture the current env configures,
// without loading any files.
func TransportSecurityFromEnv() TransportSecurity {
	env := loadTransportEnv()
	return TransportSecurity{
		TLS:       env.CertFile != "" || env.CAFile != "",
		MutualTLS: env.CertFile != "" && env.CAFile != "",
		Token:     env.Token != "",
	}
}

func (e transportEnv) validate() error {
	if (e.CertFile == "") != (e.KeyFile == "") {
		return fmt.Errorf("%s and %s must be set together", grpcTLSCertEnvVar, grpcTLSKeyEnvVar)
	}
	return nil
}

func (e transportEnv) loadCertPool() (*x509.CertPool, error) {
	pem, err := os.ReadFile(e.CAFile)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", grpcTLSCAEnvVar, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s contains no usable CA certificates", grpcTLSCAEnvVar)
	}
	return pool, nil
}

// ServerOptionsFromEnv builds the generator-side gRPC server options:
// TLS/mTLS transport credentials and bearer-token auth interceptors.
func ServerOptionsFromEnv() ([]grpc.ServerOption, error) {
	env := loadTransportEnv()
	if err := env.validate(); err != nil {
		return nil, err
	}
	if env.CAFile != "" && env.CertFile == "" {
		return nil, fmt.Errorf("%s requires a server certificate (%s/%s)", grpcTLSCAEnvVar, grpcTLSCertEnvVar, grpcTLSKeyEnvVar)
	}
	security := TransportSecurity{TLS: env.CertFile != "", MutualTLS: env.CertFile != "" && env.CAFile != "", Token: env.Token != ""}
	if security.MustRefuseInsecure(os.Getenv("APEXLOAD_ENV")) {
		return nil, fmt.Errorf("production gRPC server requires mTLS or TLS plus %s; configured %s", grpcAuthTokenEnvVar, security.String())
	}

	var options []grpc.ServerOption
	if env.CertFile != "" {
		loadConfig := func() (*tls.Config, error) {
			cert, err := tls.LoadX509KeyPair(env.CertFile, env.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("loading gRPC server certificate: %w", err)
			}
			tlsConfig := &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			}
			if env.CAFile != "" {
				pool, err := env.loadCertPool()
				if err != nil {
					return nil, err
				}
				tlsConfig.ClientCAs = pool
				tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
			}
			return tlsConfig, nil
		}
		// Fail fast on bad files at startup, then re-read per handshake so
		// cert-manager rotation takes effect without a pod restart.
		if _, err := loadConfig(); err != nil {
			return nil, err
		}
		options = append(options, grpc.Creds(credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
			GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
				return loadConfig()
			},
		})))
	}
	if env.Token != "" {
		unary, stream := tokenAuthInterceptors(env.Token)
		options = append(options, grpc.UnaryInterceptor(unary), grpc.StreamInterceptor(stream))
	}
	return options, nil
}

// DialOptionsFromEnv builds the controller-side dial options matching the
// generator's ServerOptionsFromEnv posture.
func DialOptionsFromEnv() ([]grpc.DialOption, error) {
	env := loadTransportEnv()
	if err := env.validate(); err != nil {
		return nil, err
	}

	useTLS := env.CAFile != "" || env.CertFile != ""
	security := TransportSecurity{TLS: useTLS, MutualTLS: env.CertFile != "" && env.CAFile != "", Token: env.Token != ""}
	if security.MustRefuseInsecure(os.Getenv("APEXLOAD_ENV")) {
		return nil, fmt.Errorf("production gRPC client requires mTLS or verified TLS plus %s; configured %s", grpcAuthTokenEnvVar, security.String())
	}
	options := []grpc.DialOption{
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  250 * time.Millisecond,
				Multiplier: 1.6,
				Jitter:     0.2,
				MaxDelay:   5 * time.Second,
			},
			MinConnectTimeout: 3 * time.Second,
		}),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}
	if useTLS {
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: env.ServerName,
		}
		if env.CAFile != "" {
			pool, err := env.loadCertPool()
			if err != nil {
				return nil, err
			}
			tlsConfig.RootCAs = pool
		}
		if env.CertFile != "" {
			cert, err := tls.LoadX509KeyPair(env.CertFile, env.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("loading gRPC client certificate: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}
		options = append(options, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		options = append(options, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	if env.Token != "" {
		options = append(options, grpc.WithPerRPCCredentials(bearerTokenCredentials{
			token: env.Token,
			// When TLS is on, let gRPC enforce that the token never rides a
			// plaintext channel. Without TLS we still attach it: token-only
			// auth on a trusted network beats nothing.
			secure: useTLS,
		}))
	}
	return options, nil
}

type bearerTokenCredentials struct {
	token  string
	secure bool
}

func (c bearerTokenCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{grpcAuthorizationHeader: grpcBearerPrefix + c.token}, nil
}

func (c bearerTokenCredentials) RequireTransportSecurity() bool { return c.secure }

func tokenAuthInterceptors(token string) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	authorize := func(ctx context.Context) error {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return status.Error(codes.Unauthenticated, "missing authorization metadata")
		}
		for _, value := range md.Get(grpcAuthorizationHeader) {
			candidate := strings.TrimPrefix(value, grpcBearerPrefix)
			if subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1 {
				return nil
			}
		}
		return status.Error(codes.Unauthenticated, "invalid generator auth token")
	}

	unary := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := authorize(ctx); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
	stream := func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := authorize(ss.Context()); err != nil {
			return err
		}
		return handler(srv, ss)
	}
	return unary, stream
}
