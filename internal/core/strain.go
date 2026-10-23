package core

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Strained says why the machine is too strained to start a turn, or "". It
// reads Linux's MemAvailable and memory and CPU pressure (PSI "some avg10",
// the share of the last ten seconds some task stalled); where those files
// don't exist it never holds. A limit of 0 turns its check off.
func Strained(memFloorMB int, memPressureMax, cpuPressureMax float64) string {
	return strained("/proc", memFloorMB, memPressureMax, cpuPressureMax)
}

func strained(proc string, memFloorMB int, memPressureMax, cpuPressureMax float64) string {
	if memFloorMB > 0 {
		if kb, ok := procField(proc+"/meminfo", "MemAvailable:"); ok && kb/1024 < float64(memFloorMB) {
			return fmt.Sprintf("machine low on memory (%.0f MB free, floor %d MB)", kb/1024, memFloorMB)
		}
	}
	for _, c := range []struct {
		what string
		max  float64
	}{{"memory", memPressureMax}, {"cpu", cpuPressureMax}} {
		if c.max <= 0 {
			continue
		}
		if p, ok := procField(proc+"/pressure/"+c.what, "avg10="); ok && p > c.max {
			return fmt.Sprintf("machine under %s pressure (%.2f%%, limit %g%%)", c.what, p, c.max)
		}
	}
	return ""
}

// procField reads the number after the first occurrence of key in a /proc
// file. For a pressure file that is the "some" line, deliberately: "full"
// (every task stalled at once) is rarely reached before the machine is lost.
func procField(path, key string) (float64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	_, rest, _ := strings.Cut(string(raw), key)
	f, err := strconv.ParseFloat(strings.SplitN(strings.TrimSpace(rest), " ", 2)[0], 64)
	return f, err == nil
}
