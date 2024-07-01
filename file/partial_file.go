package file

import (
	"fmt"
	"io"

	"github.com/hojdars/bitflood/types"
)

func ReadPartialFile(reader io.Reader) ([]types.Piece, error) {
	// [MVP] TODO: implement
	return []types.Piece{}, nil
}

func WritePartialFile(data []*types.Piece, writer io.Writer) error {
	for _, piece := range data {
		_, err := writer.Write(piece.Serialize())
		if err != nil {
			return fmt.Errorf("error while writing partial file, err=%s", err)
		}
	}

	return nil
}
