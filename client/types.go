package client

import (
	"fmt"

	"github.com/hojdars/bitflood/types"
)

type request struct {
	index  int
	start  int
	length int
}

type pieceProgress struct {
	order         *types.PieceOrder
	buf           []byte
	numDone       int       // how many bytes are downloaded into 'buf'
	requests      []request // queue for requests
	nextToRequest int       // which offset is the next to be requested
}

func (p *pieceProgress) DeleteRequest(start, length int) error {
	for i, r := range p.requests {
		if r.start == start && r.length == length {
			// delete the element
			p.requests[i] = p.requests[len(p.requests)-1]
			p.requests = p.requests[:len(p.requests)-1]
			return nil
		}
	}
	return fmt.Errorf("could not find request with start=%d, length=%d", start, length)
}

type seederState struct {
	requested []request
}
