package dialer

import (
	"net"
	"runtime"
	"testing"
)

func TestApply_NilIsSafe(t *testing.T) {
	Apply(nil)
}

func TestApply_LeavesOtherSystemsAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	d := &net.Dialer{}
	Apply(d)
	if d.Control != nil {
		t.Fatal("Apply set a control function")
	}
	if ContextDialer() != nil || NetDial() != nil {
		t.Fatal("a dial function was returned")
	}
}

func TestApply_DialSucceeds(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	nd := &net.Dialer{}
	Apply(nd)
	for i := 0; i < 50; i++ {
		c, err := nd.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		_ = c.Close()
	}
	_ = ln.Close()
	<-done
}
