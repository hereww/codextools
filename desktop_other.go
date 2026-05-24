//go:build !darwin

package main

func defaultManagerDesktop() bool {
	return false
}

func lockManagerDesktopThread() {}

func runManagerDesktopWindow(title, url string) error {
	return nil
}
