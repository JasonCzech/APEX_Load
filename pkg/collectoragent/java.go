package collectoragent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// The helper accepts only compiled-in query/attribute packs. Credentials travel
// on stdin and never in process arguments, environment, stderr, or artifacts.
func collectJava(ctx context.Context, target AdapterTarget, credentials Credentials) ([]analysis.MetricPoint, error) {
	if err := validateJavaTarget(target); err != nil {
		return nil, err
	}
	// A bounded line-delimited credential contract avoids adding a JSON library
	// to the Java helper. Values cannot inject additional lines.
	if strings.ContainsAny(credentials.Username+credentials.Password, "\r\n") {
		return nil, errors.New("invalid Java credential encoding")
	}
	child, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"-Xmx64m", "-XX:ActiveProcessorCount=1", "-Djava.rmi.server.useCodebaseOnly=true", "-Dcom.sun.jndi.rmi.object.trustURLCodebase=false"}
	if target.JVMTrustStore != "" {
		args = append(args, "-Djavax.net.ssl.trustStore="+target.JVMTrustStore, "-Djavax.net.ssl.trustStorePassword=changeit")
	}
	args = append(args, "-cp", target.HelperClassPath, "ApexMonitor", target.Pack, target.Destination, target.QueryPack, target.TLSCAFile)
	cmd := exec.CommandContext(child, target.JavaExecutable, args...)
	cmd.Env = []string{}
	cmd.Stdin = strings.NewReader(credentials.Username + "\n" + credentials.Password + "\n")
	output := &limitedOutput{maximum: 64 << 10}
	cmd.Stdout = output
	cmd.Stderr = &limitedOutput{maximum: 4096}
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, errors.New("restricted Java monitoring helper failed")
	}
	var points []analysis.MetricPoint
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	if decoder.Decode(&points) != nil || len(points) == 0 || len(points) > 32 {
		return nil, errors.New("invalid Java metric response")
	}
	now := time.Now().UTC()
	for i := range points {
		points[i].SchemaVersion = analysis.SchemaVersion
		points[i].ProjectID = "validation"
		points[i].Source = target.Pack
		points[i].TimestampUTC = now
		points[i].Quality = analysis.QualityValid
		if points[i].Validate() != nil {
			return nil, errors.New("invalid Java metric")
		}
		points[i].ProjectID = ""
	}
	return points, nil
}
