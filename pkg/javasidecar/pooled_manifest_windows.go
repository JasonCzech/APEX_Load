package javasidecar

import "os"

func openPooledManifestFile(path string) (*os.File, error) { return os.Open(path) }
