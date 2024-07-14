package file

import (
	"bytes"
	"testing"

	"github.com/hojdars/bitflood/assert"
	"github.com/hojdars/bitflood/types"
)

func TestSave(t *testing.T) {
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

	err := Save(&writer, data)
	assert.IsError(t, err)

	got := writer.Bytes()
	assert.Equal(t, len(got), 2*10)
	assert.SliceEqual(t, got[0:10], []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 0})
	assert.SliceEqual(t, got[10:20], []byte{1, 1, 2, 2, 3, 3, 4, 4, 5, 5})
}

func TestRead(t *testing.T) {
	want := []*types.Piece{
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
		{
			Index:  2,
			Length: 4,
			Data:   []byte{4, 4, 4, 4},
		},
	}

	data := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 4, 4, 4, 4}
	reader := bytes.NewBuffer(data)

	got, err := Read(reader, 3, 10)
	assert.IsError(t, err)
	assert.Equal(t, len(got), len(want))

	for i := 0; i < len(got); i += 1 {
		assert.Equal(t, got[i].Index, want[i].Index)
		assert.Equal(t, got[i].Length, want[i].Length)
		assert.SliceEqual(t, got[i].Data, want[i].Data)
	}
}
