//go:build race

package openaiapi

// slowdown scales the wall-clock bounds of the tests of cost: the race detector makes JSON-heavy
// code several times slower. The steps and marshals they count are the assertion; the clock is a
// coarse second one, and is not scaled away.
const slowdown = 6
