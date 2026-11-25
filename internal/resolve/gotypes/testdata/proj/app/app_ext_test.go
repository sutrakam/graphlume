package app_test

import (
	"testing"

	"example.com/proj/app"
)

// An external test package: it imports the package under test.
func TestRun(t *testing.T) {
	app.Run()
}
