package file

import (
	"fmt"
	"io"

	"github.com/hojdars/bitflood/types"
)

func Read(reader io.Reader, number, pieceLength int) ([]*types.Piece, error) {
	result := make([]*types.Piece, 0)
	for i := 0; i < number-1; i += 1 {
		piece := types.Piece{
			Index:            i,
			Length:           pieceLength,
			Data:             make([]byte, pieceLength),
			DownloadedFromId: "local",
		}
		_, err := io.ReadFull(reader, piece.Data)
		if err != nil {
			return []*types.Piece{}, fmt.Errorf("encountered error while reading piece number %d, err=%s", i, err)
		}
		result = append(result, &piece)
	}

	// last piece
	data, err := io.ReadAll(reader)
	if err != nil {
		return []*types.Piece{}, fmt.Errorf("encountered error while reading last piece, err=%s", err)
	}

	piece := types.Piece{
		Index:            number - 1,
		Length:           len(data),
		Data:             make([]byte, len(data)),
		DownloadedFromId: "local",
	}
	copy(piece.Data, data)
	result = append(result, &piece)

	return result, nil
}

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
