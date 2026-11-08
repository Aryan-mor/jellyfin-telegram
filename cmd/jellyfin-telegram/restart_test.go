package main

import (
	"os"
	"testing"
)

func TestThisProcessIsAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("this process looks dead")
	}
	if processAlive(1 << 30) {
		t.Fatal("a made-up pid looks alive")
	}
}
