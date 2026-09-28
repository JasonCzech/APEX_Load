package config

import "testing"

func TestOperatorCDSManifest(t *testing.T) {
	t.Setenv(EnvJavaVuserCDSManifest, "")
	if LoadJavaVuserOperatorConfig().CDSManifest != "" {
		t.Fatal("CDS enabled by default")
	}
	t.Setenv(EnvJavaVuserCDSManifest, " /operator/cds.json ")
	if got := LoadJavaVuserOperatorConfig().CDSManifest; got != "/operator/cds.json" {
		t.Fatal(got)
	}
}
