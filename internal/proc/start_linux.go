//go:build linux

package proc

import (
	"os"
	"strconv"
	"strings"
	"time"
)

var bootTime = func() time.Time {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(l, "btime ") {
			n, _ := strconv.ParseInt(strings.TrimSpace(l[6:]), 10, 64)
			return time.Unix(n, 0).UTC()
		}
	}
	return time.Time{}
}()

// StartTime returns when pid started, from /proc/<pid>/stat.
func StartTime(pid int) time.Time {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil || bootTime.IsZero() {
		return time.Time{}
	}
	s := string(raw)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return time.Time{}
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return time.Time{}
	}
	ticks, _ := strconv.ParseInt(f[19], 10, 64) // field 22 overall
	return bootTime.Add(time.Duration(ticks) * time.Second / 100)
}

// zombie reports a process that has exited but not been reaped.
func zombie(pid int) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	i := strings.LastIndexByte(string(raw), ')')
	return err == nil && i >= 0 && strings.HasPrefix(string(raw[i+1:]), " Z")
}
