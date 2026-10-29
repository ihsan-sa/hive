package core

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// procRoot is where Linux's figures are read; a test points it at its own.
var procRoot = "/proc"

// Strained says why the machine is too short of memory to start a turn, or
// "": Linux's MemAvailable is under memFloorMB. It is the last guard under
// the slots (slots.go), which set how much runs at once; it never moves them.
// Where /proc/meminfo doesn't exist it never holds, and a floor of 0 turns it
// off. Pressure and load no longer hold anything: they moved with every
// reading, and a pace set from them retuned itself all day (docs/log.md,
// 2026-10-29).
func Strained(memFloorMB int) string { return strained(procRoot, memFloorMB) }

func strained(proc string, memFloorMB int) string {
	if memFloorMB > 0 {
		if kb, ok := procField(proc+"/meminfo", "MemAvailable:"); ok && kb/1024 < float64(memFloorMB) {
			return fmt.Sprintf("machine low on memory (%.0f MB free, floor %d MB)", kb/1024, memFloorMB)
		}
	}
	return ""
}

// procField reads the number after the first occurrence of key in a /proc
// file.
func procField(path, key string) (float64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	_, rest, _ := strings.Cut(string(raw), key)
	f, err := strconv.ParseFloat(strings.SplitN(strings.TrimSpace(rest), " ", 2)[0], 64)
	return f, err == nil
}
