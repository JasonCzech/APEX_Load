package api

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/config"
)

type VersionResponse struct {
	buildinfo.Info
	Deployment DeploymentIdentity `json:"deployment"`
}

type DeploymentIdentity struct {
	Kind                   string `json:"kind"`
	Label                  string `json:"label"`
	BindScope              string `json:"bind_scope"`
	AuthEnforced           bool   `json:"auth_enforced"`
	DesktopAvailable       bool   `json:"desktop_available"`
	ClientDownloadsEnabled bool   `json:"client_downloads_enabled"`
}

func (s *Server) versionResponse() VersionResponse {
	info := buildinfo.Get()
	return VersionResponse{
		Info:       info,
		Deployment: s.deploymentIdentity(info),
	}
}

func (s *Server) deploymentIdentity(info buildinfo.Info) DeploymentIdentity {
	settings := s.runner.GetSystemSettings()
	bindScope := strings.TrimSpace(s.bindScope)
	if bindScope == "" && s.httpServer != nil {
		bindScope = config.ClassifyBindScope(s.httpServer.Addr)
	}
	if bindScope == "" {
		bindScope = config.BindScopeWide
	}
	kind, label := s.deploymentKindAndLabel(info.Environment, bindScope)
	auth := normalizeAuthSettings(settings.Auth)
	return DeploymentIdentity{
		Kind:                   kind,
		Label:                  label,
		BindScope:              bindScope,
		AuthEnforced:           !isAuthBypassMode(auth.Mode),
		DesktopAvailable:       clientAppAvailable(settings.ClientApp),
		ClientDownloadsEnabled: clientDownloadsEnabled(settings.ClientDownloads),
	}
}

func (s *Server) deploymentKindAndLabel(environment string, bindScope string) (string, string) {
	if s.desktopMode {
		return "desktop", "Desktop"
	}
	env := strings.ToLower(strings.TrimSpace(environment))
	switch env {
	case "kubernetes", "k8s", "aks", "eks", "gke":
		return "kubernetes", "Kubernetes"
	case "docker", "container":
		return "docker", "Docker"
	case "local", "dev", "development":
		if bindScope == config.BindScopeLoopback {
			return "local-server", "Local server"
		}
		return "server", "Server"
	}
	if bindScope == config.BindScopeLoopback {
		return "local-server", "Local server"
	}
	return "server", "Server"
}
