//go:build windows

package main

import (
	"path/filepath"

	webview "github.com/jchv/go-webview2"
)

func defaultManagerDesktop() bool {
	return true
}

func lockManagerDesktopThread() {}

func runManagerDesktopWindow(title, url string) error {
	w := webview.NewWithOptions(webview.WebViewOptions{
		AutoFocus: true,
		DataPath:  filepath.Join(stateDir(), "webview2"),
		WindowOptions: webview.WindowOptions{
			Title:  title,
			Width:  1180,
			Height: 780,
			IconId: 1,
			Center: true,
		},
	})
	if w == nil {
		return openURL(url)
	}
	defer w.Destroy()
	w.SetSize(960, 640, webview.HintMin)
	w.Navigate(url)
	w.Run()
	return nil
}
