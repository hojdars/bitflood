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

func WritePartialFiles(writers []io.Writer, data []*types.Piece, savedPieces *bitfield.Bitfield) error {
	for _, piece := range data {
		if piece == nil {
			continue
		}

		alreadySaved, err := savedPieces.Get(piece.Index)
		if err != nil {
			return fmt.Errorf("error while verifying piece index in bitfield, err=%s", err)
		}
		if alreadySaved {
			continue
		}

		fileIndex := piece.Index / 1000
		if fileIndex >= len(writers) {
			return fmt.Errorf("error while writing partial files, piece index %d requires part %d but only %d writers were provided",
				piece.Index, fileIndex, len(writers))
		}

		_, err = writers[fileIndex].Write(piece.Serialize())
		if err != nil {
			return fmt.Errorf("error while writing partial file, err=%s", err)
		}
	}

	return nil
}
