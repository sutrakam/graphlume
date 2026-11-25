package shop

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type failure struct{}

func (f failure) Error() string { return "" } // def failure.Error

func TestRun(t *testing.T) {
	require.Error(t, failure{}) // call Error -> external
}
