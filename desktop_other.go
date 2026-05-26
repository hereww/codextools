//go:build !darwin && !windows

package main

func defaultManagerDesktop() bool {
	return false
}

func lockManagerDesktopThread() {}

func runManagerDesktopWindow(title, url string) error {
	return nil
}
