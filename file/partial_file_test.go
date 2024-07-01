package file

import (
	"bytes"
	"testing"

	"github.com/hojdars/bitflood/assert"
	"github.com/hojdars/bitflood/types"
)

const PieceByteLength = 18

func TestPartialFileWrite(t *testing.T) {
	data := []*types.Piece{
		{
			Index:  0,
			Length: 10,
			Data:   []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 0},
		},
		{
			Index:  1,
			Length: 10,
			Data:   []byte{1, 1, 2, 2, 3, 3, 4, 4, 5, 5},
		},
	}

	var writer bytes.Buffer

	err := WritePartialFile(data, &writer)
	assert.IsError(t, err)

	got := writer.Bytes()
	assert.Equal(t, len(got), 2*PieceByteLength)
	assert.SliceEqual(t, got[0:PieceByteLength], []byte{0, 0, 0, 0, 0, 0, 0, 10, 1, 2, 3, 4, 5, 6, 7, 8, 9, 0})
	assert.SliceEqual(t, got[PieceByteLength:2*PieceByteLength], []byte{0, 0, 0, 1, 0, 0, 0, 10, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5})
}
