package bitfield

import "fmt"

type Bitfield struct {
	Length int
	data   []byte
}

func New(length int) Bitfield {
	lenBytes := (length / 8) + 1
	return Bitfield{Length: length, data: make([]byte, lenBytes)}
}

func FromBytes(bytes []byte, length int) Bitfield {
	return Bitfield{Length: length, data: bytes}
}

func Copy(in *Bitfield) Bitfield {
	result := Bitfield{}
	result.Length = in.Length
	result.data = make([]byte, len(in.data))
	copy(result.data, in.data)
	return result
}

func (field *Bitfield) Get(index int) (bool, error) {
	if index >= field.Length {
		return false, fmt.Errorf("Get: index out of bounds, index=%d, bitfield length=%d", index, field.Length)
	}

	byteIndex := index / 8
	bitIndex := index % 8
	mask := 1 << (7 - bitIndex)
	value := field.data[byteIndex] & byte(mask)
	return value != 0, nil
}

func (field *Bitfield) Set(index int, value bool) error {
	if index >= field.Length {
		return fmt.Errorf("Set: index out of bounds, index=%d, bitfield length=%d", index, field.Length)
	}

	byteIndex := index / 8
	bitIndex := index % 8
	mask := byte(1 << (7 - bitIndex))

	if value {
		field.data[byteIndex] |= byte(mask)
		return nil
	} else {
		mask = ^mask
		field.data[byteIndex] &= byte(mask)
		return nil
	}
}
