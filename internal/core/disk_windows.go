package core

import "golang.org/x/sys/windows"

// statRoom reads the free space (this user's, quotas counted) and size of the
// volume holding path.
func statRoom(path string) (free, size int64, ok bool) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, false
	}
	var avail, total, all uint64
	if windows.GetDiskFreeSpaceEx(p, &avail, &total, &all) != nil {
		return 0, 0, false
	}
	return int64(avail), int64(total), true
}

// quotaShown says statRoom's figures count a per-user quota: on Windows
// GetDiskFreeSpaceEx's free space does.
const quotaShown = true
