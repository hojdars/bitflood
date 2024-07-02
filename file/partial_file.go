package file

import (
	"fmt"
	"io"

	"github.com/hojdars/bitflood/bitfield"
	"github.com/hojdars/bitflood/types"
)

func ReadPartialFile(reader io.Reader, bitfield *bitfield.Bitfield) ([]types.Piece, error) {
	result := make([]types.Piece, 0)
	for {
		var piece types.Piece
		err := piece.Deserialize(reader)
		if err == io.EOF {
			break
		} else if err != nil {
			return []types.Piece{}, fmt.Errorf("error while loading PartialFile, err=%s", err)
		}

		result = append(result, piece)
		bitfield.Set(piece.Index, true)
	}

	return result, nil
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
