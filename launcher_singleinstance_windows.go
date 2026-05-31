//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

type windowsLauncherSingleInstanceLock struct {
	handle windows.Handle
	owned  bool
}

func acquireLauncherSingleInstanceLock(debugPort uint16) (launcherSingleInstanceLock, bool, error) {
	name := fmt.Sprintf(`Local\CodexToolsLauncher_%d`, debugPort)
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, err
	}
	handle, err := windows.CreateMutex(nil, true, namePtr)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			if handle != 0 {
				_ = windows.CloseHandle(handle)
			}
			return nil, false, nil
		}
		return nil, false, err
	}
	return &windowsLauncherSingleInstanceLock{handle: handle, owned: true}, true, nil
}

func (lock *windowsLauncherSingleInstanceLock) release() {
	if lock == nil || lock.handle == 0 {
		return
	}
	if lock.owned {
		_ = windows.ReleaseMutex(lock.handle)
	}
	_ = windows.CloseHandle(lock.handle)
	lock.handle = 0
	lock.owned = false
}
