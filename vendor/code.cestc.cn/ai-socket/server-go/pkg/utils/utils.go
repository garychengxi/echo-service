package utils

import "strconv"

func JoinInt(numbers ...int) int {
	var text string
	for _, n := range numbers {
		text += strconv.Itoa(n)
	}
	result, err := strconv.Atoi(text)
	if err != nil {
		return 0
	}
	return result
}
