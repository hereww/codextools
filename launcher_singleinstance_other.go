//go:build !windows

package main

func acquireLauncherSingleInstanceLock(debugPort uint16) (launcherSingleInstanceLock, bool, error) {
	return noopLauncherSingleInstanceLock{}, true, nil
}

type noopLauncherSingleInstanceLock struct{}

func (noopLauncherSingleInstanceLock) release() {}
