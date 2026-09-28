package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFacadeConfigRejectsUnsafeBindingsAndUnknownFields(t *testing.T) {
	good := facadeConfig{Contract: "apexload.java-facade/v1", Listen: "127.0.0.1:8092", InvocationMS: 1000, Certificate: "/tls/cert", Key: "/tls/key", CA: "/tls/ca", TokenFile: "/token"}
	for _, addr := range []string{"0.0.0.0:8092", "localhost:8092", "10.0.0.1:8092", "127.0.0.1:0", "127.0.0.1:99999", "127.0.0.1:8092", "[::1]:8092"} {
		c := good
		c.Listen = addr
		b, _ := json.Marshal(c)
		path := filepath.Join(t.TempDir(), "config")
		if e := os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
		_, e := loadConfig(path)
		want := addr == "127.0.0.1:8092" || addr == "[::1]:8092"
		if (e == nil) != want {
			t.Fatal(addr, e)
		}
	}
	b, _ := json.Marshal(good)
	b = append(b[:len(b)-1], []byte(`,"unknown":true}`)...)
	path := filepath.Join(t.TempDir(), "config")
	_ = os.WriteFile(path, b, 0600)
	if _, e := loadConfig(path); e == nil {
		t.Fatal("unknown field")
	}
}
func TestFacadeBoundedFilesAndArguments(t *testing.T) {
	if _, e := readBounded("relative", 10); e == nil {
		t.Fatal("relative file")
	}
	if _, e := readBounded(t.TempDir(), 10); e == nil {
		t.Fatal("directory")
	}
	path := filepath.Join(t.TempDir(), "large")
	_ = os.WriteFile(path, make([]byte, 11), 0600)
	if _, e := readBounded(path, 10); e == nil {
		t.Fatal("oversize")
	}
	for _, args := range [][]string{{}, {"-unknown"}, {"extra"}} {
		if e := run(context.Background(), args); e == nil {
			t.Fatal(args)
		}
	}
}
