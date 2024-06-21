package bitfield

import (
	"fmt"
	"testing"
)

func TestBitfield(t *testing.T) {

	t.Run("make the bitfield", func(t *testing.T) {
		bitfield := New(10)
		assertEqual(t, bitfield.Length, 10)
	})

	t.Run("empty bitfield should have 'false' set everywhere", func(t *testing.T) {
		bitfield := New(10)

		got, err := bitfield.Get(0)
		assertErr(t, err)
		assertEqual(t, got, false)

		got, err = bitfield.Get(2)
		assertErr(t, err)
		assertEqual(t, got, false)

		got, err = bitfield.Get(9)
		assertErr(t, err)
		assertEqual(t, got, false)
	})

	t.Run("getting out of bounds should be error", func(t *testing.T) {
		bitfield := New(10)
		_, err := bitfield.Get(10)
		assertEqual(t, err.Error(), fmt.Errorf("Get: index out of bounds, index=%d, bitfield length=%d", 10, 10).Error())
	})

	t.Run("get after set should return the set value", func(t *testing.T) {
		bitfield := New(10)
		err := bitfield.Set(0, true)
		assertErr(t, err)
		err = bitfield.Set(5, true)
		assertErr(t, err)

		got, err := bitfield.Get(0)
		assertEqual(t, got, true)
		assertErr(t, err)

		got, err = bitfield.Get(5)
		assertEqual(t, got, true)
		assertErr(t, err)
	})

	t.Run("setting out of bounds should be error", func(t *testing.T) {
		bitfield := New(10)
		err := bitfield.Set(10, true)
		assertEqual(t, err.Error(), fmt.Errorf("Set: index out of bounds, index=%d, bitfield length=%d", 10, 10).Error())
	})

	t.Run("complex Get test", func(t *testing.T) {
		data := []byte{0b11110100, 0b01010100} // last two '0' are unused, length is 14
		bitfield := Bitfield{Length: 14, data: data}

		outputs := []bool{true, true, true, true, false, true, false, false, false, true, false, true, false, true}
		for i := 0; i < len(outputs); i++ {
			got, err := bitfield.Get(i)
			assertErr(t, err)
			assertEqual(t, outputs[i], got)
		}
		for i := len(outputs); i < len(outputs)+4; i++ {
			_, err := bitfield.Get(i)
			assertEqual(t, err.Error(), fmt.Errorf("Get: index out of bounds, index=%d, bitfield length=%d", i, 14).Error())
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

		assertEqual(t, bitfield.data[0], first)
		assertEqual(t, bitfield.data[1], second)
	})
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got '%v', wanted '%v'", got, want)
	}
}

func assertErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("got error, err=%s", err)
	}
}
