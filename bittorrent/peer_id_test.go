package bittorrent

import (
	"fmt"
	"testing"
)

func TestMakePeerId(t *testing.T) {
	peerId := MakePeerId()
	if len(peerId) != 20 {
		t.Errorf("peerId has wrong length, wanted 20, got %d", len(peerId))
	}
	want := fmt.Sprintf("%s-", ClientId)
	if peerId[0:5] != want {
		t.Errorf("got %s, wanted %s", peerId[0:5], want)
	}
}
