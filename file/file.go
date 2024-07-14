package file

import (
	"fmt"
	"io"

	"github.com/hojdars/bitflood/types"
)

func Save(writer io.Writer, data []*types.Piece) error {
	for i, piece := range data {
		if i != piece.Index {
			panic(fmt.Sprintf("data on index %d has piece of with a different index %d", i, piece.Index))
		}

		_, err := writer.Write(piece.Data)
		if err != nil {
			return fmt.Errorf("encountered error while writing piece index %d, err=%s", i, err)
		}
	}
	return nil
}
