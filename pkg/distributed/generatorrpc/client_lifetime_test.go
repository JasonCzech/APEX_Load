package generatorrpc

import (
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

func TestRetainedClientsOwnIndependentTransportLifetimes(t *testing.T) {
	conn, err := grpc.NewClient("passthrough:///unused", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	root := NewClient(conn)
	first, err := root.Retain()
	if err != nil {
		t.Fatal(err)
	}
	second, err := root.Retain()
	if err != nil {
		t.Fatal(err)
	}
	_ = root.Close()
	var closers sync.WaitGroup
	for range 8 {
		closers.Go(func() { _ = first.Close() })
	}
	closers.Wait()
	if conn.GetState() == connectivity.Shutdown {
		t.Fatal("closing one run disconnected the other retained run")
	}
	if _, err := first.Retain(); err == nil {
		t.Fatal("retained an already closed handle")
	}
	_ = second.Close()
	if conn.GetState() != connectivity.Shutdown {
		t.Fatal("the final run did not release the transport")
	}
}
