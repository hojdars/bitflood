package bitfield

import (
	"fmt"
	"testing"

	"github.com/hojdars/bitflood/assert"
)

func TestBitfield(t *testing.T) {

	t.Run("make the bitfield", func(t *testing.T) {
		bitfield := New(10)
		assert.Equal(t, bitfield.Length, 10)
	})

	t.Run("empty bitfield should have 'false' set everywhere", func(t *testing.T) {
		bitfield := New(10)

		got, err := bitfield.Get(0)
		assert.IsError(t, err)
		assert.Equal(t, got, false)

		got, err = bitfield.Get(2)
		assert.IsError(t, err)
		assert.Equal(t, got, false)

		got, err = bitfield.Get(9)
		assert.IsError(t, err)
		assert.Equal(t, got, false)
	})

	t.Run("getting out of bounds should be error", func(t *testing.T) {
		bitfield := New(10)
		_, err := bitfield.Get(10)
		assert.Equal(t, err.Error(), fmt.Errorf("Get: index out of bounds, index=%d, bitfield length=%d", 10, 10).Error())
	})

	t.Run("get after set should return the set value", func(t *testing.T) {
		bitfield := New(10)
		err := bitfield.Set(0, true)
		assert.IsError(t, err)
		err = bitfield.Set(5, true)
		assert.IsError(t, err)

		got, err := bitfield.Get(0)
		assert.Equal(t, got, true)
		assert.IsError(t, err)

		got, err = bitfield.Get(5)
		assert.Equal(t, got, true)
		assert.IsError(t, err)
	})

	t.Run("setting out of bounds should be error", func(t *testing.T) {
		bitfield := New(10)
		err := bitfield.Set(10, true)
		assert.Equal(t, err.Error(), fmt.Errorf("Set: index out of bounds, index=%d, bitfield length=%d", 10, 10).Error())
	})

	t.Run("complex Get test", func(t *testing.T) {
		data := []byte{0b11110100, 0b01010100} // last two '0' are unused, length is 14
		bitfield := Bitfield{Length: 14, data: data}

		outputs := []bool{true, true, true, true, false, true, false, false, false, true, false, true, false, true}
		for i := 0; i < len(outputs); i++ {
			got, err := bitfield.Get(i)
			assert.IsError(t, err)
			assert.Equal(t, outputs[i], got)
		}
		for i := len(outputs); i < len(outputs)+4; i++ {
			_, err := bitfield.Get(i)
			assert.Equal(t, err.Error(), fmt.Errorf("Get: index out of bounds, index=%d, bitfield length=%d", i, 14).Error())
		}
	})

	t.Run("complex Set test", func(t *testing.T) {
		data := []byte{0b11110100, 0b01010100} // last two '0' are unused, length is 14
		bitfield := Bitfield{Length: 14, data: data}

		bitfield.Set(0, false)
		bitfield.Set(1, false)
		bitfield.Set(2, false)
		bitfield.Set(3, false)
		bitfield.Set(4, true)
		first := byte(0b00001100)

		bitfield.Set(8, true)
		bitfield.Set(9, false)
		bitfield.Set(10, true)
		bitfield.Set(11, false)
		second := byte(0b10100100)

		assert.Equal(t, bitfield.data[0], first)
		assert.Equal(t, bitfield.data[1], second)
	})
}
