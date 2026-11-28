package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuestUpdateRequiresExactConfirmation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready")
	commit := strings.Repeat("a", 40)
	exit := make(chan error, 1)
	exit <- nil
	if ok, _, ended := awaitGuestUpdate(context.Background(), path, commit, exit); ok || !ended {
		t.Fatal("unconfirmed early exit accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("b", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	if guestUpdateConfirmed(path, commit) {
		t.Fatal("wrong revision confirmed")
	}
	if err := os.WriteFile(path, []byte(commit), 0600); err != nil {
		t.Fatal(err)
	}
	exit <- nil
	if ok, _, ended := awaitGuestUpdate(context.Background(), path, commit, exit); !ok || !ended {
		t.Fatal("confirmed exit not recognized")
	}
}
