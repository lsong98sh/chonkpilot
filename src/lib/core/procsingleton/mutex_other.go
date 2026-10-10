//go:build !windows

package procsingleton

// Acquire 在非 Windows 平台不做进程单例（恒为首实例；server 仅发行于 Windows）。
func Acquire(user string) (release func(), duplicate bool, err error) {
	return func() {}, false, nil
}
