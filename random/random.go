package random

import "math/rand"

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func MakeRandomString(length int) string {
	result := make([]rune, length)
	for i := range result {
		result[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(result)
}

func RandomizeAnnounceList(announceList [][]string) (result [][]string) {
	result = make([][]string, len(announceList))
	for i := 0; i < len(announceList); i += 1 {
		result[i] = make([]string, len(announceList[i]))
		if len(announceList[i]) == 1 {
			result[i][0] = announceList[i][0]
			continue
		}

		indeces := rand.Perm(len(announceList[i]))
		for pos := 0; pos < len(announceList[i]); pos += 1 {
			result[i][indeces[pos]] = announceList[i][pos]
		}
	}
	return
}
