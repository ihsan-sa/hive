//go:build !linux

package gate

func daemonsUnder(string) []int { return nil }
