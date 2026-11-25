package app

import "testing"

// An in-package test: it sees unexported names.
func TestHandlerName(t *testing.T) {
	if handlerName() != "here" {
		t.Fatal("platform")
	}
}
