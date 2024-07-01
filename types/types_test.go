package types

import (
	"bytes"
	"testing"

	"github.com/hojdars/bitflood/assert"
)

func TestPiece(t *testing.T) {
	t.Run("test Piece serialization", func(t *testing.T) {
		piece := Piece{
			Index:  0,
			Length: 10,
			Data:   []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 0},
		}

		got := piece.Serialize()
		want := []byte{0, 0, 0, 0, 0, 0, 0, 10, 1, 2, 3, 4, 5, 6, 7, 8, 9, 0}

		assert.SliceEqual(t, got, want)
	})

	t.Run("test Piece deserialization", func(t *testing.T) {
		data := []byte{0, 0, 0, 0, 0, 0, 0, 10, 1, 2, 3, 4, 5, 6, 7, 8, 9, 0}
		dataRdr := bytes.NewReader(data)
		got := Piece{}
		got.Deserialize(dataRdr)

		assert.Equal(t, got.Index, 0)
		assert.Equal(t, got.Length, 10)
		assert.SliceEqual(t, got.Data, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 0})
	})

}
