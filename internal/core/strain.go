package core

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Strained says why the machine is too strained to start a turn, or "". It
// reads Linux's MemAvailable and memory pressure (PSI "some avg10", the share
// of the last ten seconds some task stalled on memory); where those files
// don't exist it never holds. A floor of 0 turns its check off.
func Strained(memFloorMB int, pressureMax float64) string {
	return strained("/proc", memFloorMB, pressureMax)
}

func strained(proc string, memFloorMB int, pressureMax float64) string {
	if memFloorMB > 0 {
		if kb, ok := procField(proc+"/meminfo", "MemAvailable:"); ok && kb/1024 < float64(memFloorMB) {
			return fmt.Sprintf("machine low on memory (%.0f MB free, floor %d MB)", kb/1024, memFloorMB)
		}
	}
	if pressureMax > 0 {
		if p, ok := procField(proc+"/pressure/memory", "avg10="); ok && p > pressureMax {
			return fmt.Sprintf("machine under memory pressure (%.2f%%, limit %g%%)", p, pressureMax)
		}
	}
	return ""
}

// procField reads the number after the first occurrence of key in a /proc file.
func procField(path, key string) (float64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	_, rest, _ := strings.Cut(string(raw), key)
	f, err := strconv.ParseFloat(strings.SplitN(strings.TrimSpace(rest), " ", 2)[0], 64)
	return f, err == nil
}
