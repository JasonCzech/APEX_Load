// Package fixture exposes the configurable mock LLM streaming and
// continuous-batching HTTP server for cmd/apexllm-target.
package fixture

import "github.com/jasonczech/apexload/pkg/internal/llmfixture"

type (
	Dialect         = llmfixture.Dialect
	FaultKind       = llmfixture.FaultKind
	FaultConfig     = llmfixture.FaultConfig
	SimulatorConfig = llmfixture.SimulatorConfig
	Config          = llmfixture.Config
	Server          = llmfixture.Server
)

const DialectOpenAIChat = llmfixture.DialectOpenAIChat

// NewServer creates a new mock LLM HTTP server.
func NewServer(cfg Config) *Server {
	return llmfixture.NewServer(cfg)
}
