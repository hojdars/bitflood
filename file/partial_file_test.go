package file

import (
	"bytes"
	"testing"

	"github.com/hojdars/bitflood/assert"
	"github.com/hojdars/bitflood/bitfield"
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

func TestPartialFileRead(t *testing.T) {
	data := []byte{0, 0, 0, 0, 0, 0, 0, 10, 1, 2, 3, 4, 5, 6, 7, 8, 9, 0, 0, 0, 0, 1, 0, 0, 0, 10, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5}
	reader := bytes.NewBuffer(data)

	bitfield := bitfield.New(3)
	got, err := ReadPartialFile(reader, &bitfield)

	assert.IsError(t, err)
	assert.Equal(t, len(got), 2)

	gotPiece := got[0]
	assert.Equal(t, gotPiece.Index, 0)
	assert.Equal(t, gotPiece.Length, 10)
	assert.SliceEqual(t, gotPiece.Data, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 0})
	v, err := bitfield.Get(0)
	assert.IsError(t, err)
	assert.Equal(t, v, true)

	gotPiece = got[1]
	assert.Equal(t, gotPiece.Index, 1)
	assert.Equal(t, gotPiece.Length, 10)
	assert.SliceEqual(t, gotPiece.Data, []byte{1, 1, 2, 2, 3, 3, 4, 4, 5, 5})
	v, err = bitfield.Get(1)
	assert.IsError(t, err)
	assert.Equal(t, v, true)

	v, err = bitfield.Get(2)
	assert.IsError(t, err)
	assert.Equal(t, v, false)
}
