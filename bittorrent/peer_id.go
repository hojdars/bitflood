package bittorrent

import (
	"fmt"

	"github.com/hojdars/bitflood/random"
)

const ClientId string = "SH01"

func MakePeerId() string {
	return fmt.Sprintf("%s-%s", ClientId, random.MakeRandomString(20-len(ClientId)-1))
}
