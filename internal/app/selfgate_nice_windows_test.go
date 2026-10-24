//go:build windows

package app

import "errors"

func setSelfNice(int) error { return errors.New("no nice levels on Windows") }
