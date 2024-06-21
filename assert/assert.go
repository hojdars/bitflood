package assert

import (
	"slices"
	"testing"
)

func Equal[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got '%v', wanted '%v'", got, want)
	}
}

func IsError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("got error, err=%s", err)
	}
}

func SliceEqual(t *testing.T, got, want []byte) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("got '%v' (len=%v), wanted '%v'(len=%v)", got, len(got), want, len(want))
	}
}
