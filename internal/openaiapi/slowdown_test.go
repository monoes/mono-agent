//go:build !race

package openaiapi

// slowdown scales the wall-clock bounds of the tests of cost (see slowdown_race_test.go).
const slowdown = 1
