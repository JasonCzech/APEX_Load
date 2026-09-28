package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/perfevidence"
)

func runJava(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: apexbench java fixture|target|inspect|baseline|pilot|compare [flags]")
	}
	fs := flag.NewFlagSet("apexbench java "+args[0], flag.ContinueOnError)
	output := fs.String("output", "", "explicit JSON output path; default stdout")
	overwrite := fs.Bool("overwrite", false, "replace explicitly named output")
	var value any
	var operationErr error
	switch args[0] {
	case "inspect":
		var paths []string
		fs.Func("artifact", "repeat for application source/class/JAR/ZIP (never JDK internals)", func(path string) error { paths = append(paths, path); return nil })
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || len(paths) == 0 {
			return errors.New("native inspection requires --artifact paths")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		report, err := javarunner.InspectApplicationNative(ctx, paths)
		value, operationErr = report, err
	case "fixture":
		workload := fs.String("workload", "light", "light or middleware-heavy")
		endpoint := fs.String("endpoint", "http://127.0.0.1:18099", "fixture target origin")
		var classpath []string
		fs.Func("classpath", "repeat for each exact fixture/dependency JAR", func(path string) error { classpath = append(classpath, path); return nil })
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("unexpected fixture arguments")
		}
		script, err := perfevidence.JavaFixtureScript(*workload, *endpoint, classpath)
		if err != nil {
			return err
		}
		value = map[string]any{"script": script}
	case "target":
		addr := fs.String("addr", "127.0.0.1:18099", "loopback fixture target address; run outside benchmark cgroup")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || *output != "" || *overwrite {
			return errors.New("unexpected target arguments")
		}
		host, _, err := net.SplitHostPort(*addr)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return errors.New("fixture target must bind a loopback IP")
		}
		listener, err := net.Listen("tcp", *addr)
		if err != nil {
			return err
		}
		server := &http.Server{Handler: perfevidence.JavaFixtureTarget(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		go func() {
			<-ctx.Done()
			shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_ = server.Shutdown(shutdown)
		}()
		fmt.Fprintln(os.Stderr, "Java fixture target:", listener.Addr())
		err = server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case "baseline", "pilot":
		var cfg perfevidence.JavaRunConfig
		var manifest *string
		var preparation *time.Duration
		profileDefault := "dedicated-default"
		if args[0] == "pilot" {
			manifest = fs.String("pilot-manifest", "", "operator-approved runner manifest, same local assets and file as runner")
			preparation = fs.Duration("preparation-timeout", 2*time.Minute, "independent cold preparation deadline")
			profileDefault = "pooled-pilot"
		}
		fs.StringVar(&cfg.ScriptPath, "script", "", "precompiled persistent Java fixture DSL")
		fs.StringVar(&cfg.CgroupPath, "cgroup", "", "exclusive cgroup-v2 subtree containing benchmark and runner, excluding SUT")
		fs.StringVar(&cfg.Workload, "workload", "", "workload label: light or middleware-heavy")
		fs.StringVar(&cfg.Profile, "profile", profileDefault, "operator configuration label")
		fs.StringVar(&cfg.RuntimeSHA256, "runtime-sha256", "", "SHA-256 of exact JDK/harness/dependency identity manifest")
		fs.StringVar(&cfg.Environment, "environment", "", "controlled hardware/image identity")
		fs.StringVar(&cfg.ResourceProfile, "resources", "", "resource-limits identity")
		fs.StringVar(&cfg.Target, "target", "", "SUT identity; excludes secret-bearing URLs")
		fs.IntVar(&cfg.RunnerPID, "runner-pid", 0, "existing dedicated sidecar PID in this PID namespace")
		fs.IntVar(&cfg.VUsers, "vusers", 25, "persistent VUsers")
		fs.IntVar(&cfg.Repetitions, "repetitions", 5, "repeated runs, 1 through 5")
		fs.DurationVar(&cfg.Soak, "soak", 0, "optional continuous soak after five repetitions (1h to 2h; at least 3 Vusers)")
		fs.DurationVar(&cfg.Warmup, "warmup", 40*time.Second, "warm-up after every VUser completes initialization")
		fs.DurationVar(&cfg.Measurement, "duration", time.Minute, "steady measurement interval")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("unexpected Java benchmark arguments")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		var report perfevidence.JavaEvidence
		var err error
		if args[0] == "pilot" {
			report, err = perfevidence.RunJavaPooled(ctx, perfevidence.JavaPilotRunConfig{JavaRunConfig: cfg, ManifestPath: *manifest, PreparationTimeout: *preparation})
		} else {
			report, err = perfevidence.RunJavaDedicated(ctx, cfg)
		}
		if report.Schema == "" {
			return err
		}
		value = report
		operationErr = err
	case "compare":
		baselinePath := fs.String("baseline", "", "validated dedicated JSON evidence")
		candidatePath := fs.String("candidate", "", "pooled JSON evidence")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("unexpected Java comparison arguments")
		}
		baseline, err := readJavaReport(*baselinePath)
		if err != nil {
			return err
		}
		candidate, err := readJavaReport(*candidatePath)
		if err != nil {
			return err
		}
		gate := perfevidence.EvaluateJavaGate(baseline, candidate)
		value = gate
		if gate.Status != "pass" {
			operationErr = fmt.Errorf("Java evidence gate: %s", gate.Status)
		}
	default:
		return errors.New("Java evidence operation must be fixture, target, inspect, baseline, pilot, or compare")
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if *output != "" {
		err = writeOutput(*output, data, *overwrite)
	} else {
		_, err = os.Stdout.Write(data)
	}
	if err != nil {
		return err
	}
	return operationErr
}

func readJavaReport(path string) (perfevidence.JavaEvidence, error) {
	f, err := os.Open(path)
	if err != nil {
		return perfevidence.JavaEvidence{}, err
	}
	defer f.Close()
	return perfevidence.ReadJavaEvidence(f)
}
