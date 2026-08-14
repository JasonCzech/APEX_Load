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
	kind, label := s.deploymentKindAndLabel(info.Environment)
	auth := s.authSettingsForRequest()
	return DeploymentIdentity{
		Kind:                   kind,
		Label:                  label,
		BindScope:              bindScope,
		AuthEnforced:           !isAuthBypassMode(auth.Mode),
		DesktopAvailable:       clientAppAvailable(settings.ClientApp),
		ClientDownloadsEnabled: clientDownloadsEnabled(settings.ClientDownloads),
	}
}

func (s *Server) deploymentKindAndLabel(environment string) (string, string) {
	// The Wails desktop client is the only "local" surface; it is detected by
	// desktopMode and labeled "Local engine" in the UI.
	if s.desktopMode {
		return "desktop", "Desktop"
	}
	// Every other surface is a browser connected to a (remote) server. The only
	// supported browser configuration is browser -> remote Docker/Azure server,
	// so a non-desktop controller is always a "server" regardless of bind scope —
	// a browser is never a "local server".
	env := strings.ToLower(strings.TrimSpace(environment))
	switch env {
	case "kubernetes", "k8s", "aks", "eks", "gke":
		return "kubernetes", "Kubernetes"
	case "azure_container_apps", "azure-container-apps", "container_apps":
		return "azure_container_apps", "Azure Container Apps"
	case "aws_ecs_fargate", "aws-ecs-fargate", "ecs_fargate":
		return "aws_ecs_fargate", "AWS ECS Fargate"
	case "docker", "container":
		return "docker", "Docker"
	}
	return "server", "Server"
}
