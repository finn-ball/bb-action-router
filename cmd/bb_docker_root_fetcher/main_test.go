package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListenUnixSocketMode(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "fetcher.sock")
	listener, err := listenUnixSocket(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o660); got != want {
		t.Errorf("Socket permissions are %#o, want %#o", got, want)
	}
}

func TestMaterializeInvalidImageReference(t *testing.T) {
	for _, ref := range []string{"", "image:latest", "image@sha256:invalid"} {
		t.Run(ref, func(t *testing.T) {
			m := &materializer{rootsDir: t.TempDir()}
			// No puller is needed: validation must happen before downloading.
			_, err := m.materialize(context.Background(), ref)
			if err == nil || !strings.Contains(err.Error(), "invalid image ref") {
				t.Fatalf("materialize(%q) error = %v, want invalid image ref", ref, err)
			}
			entries, err := os.ReadDir(m.rootsDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("invalid reference left temporary entries: %v", entries)
			}
		})
	}
}
