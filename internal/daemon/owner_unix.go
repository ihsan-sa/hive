//go:build !windows

package daemon

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// fileOwner names the user that owns path, empty if it can't be read.
func fileOwner(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fileOwnerOf(int(st.Uid))
}

// fileOwnerOf names uid, with its user name where there is one.
func fileOwnerOf(uid int) string {
	s := "uid " + strconv.Itoa(uid)
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		s = u.Username + " (" + s + ")"
	}
	return s
}
