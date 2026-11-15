package browser

import "os"

func DefaultEndpoint() string {
	return `\\.\pipe\org.keepassxc.KeePassXC.BrowserServer_` + os.Getenv("USERNAME")
}
