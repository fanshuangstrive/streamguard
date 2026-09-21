//go:build windows

package main

import "os"

// notifyReloadSignal 在 Windows 平台为空实现。
//
// Windows 没有 SIGHUP 语义，配置热重载通过 GUI 的「保存并重启」
// 或重启进程完成。
func notifyReloadSignal(ch chan<- os.Signal) {
	_ = ch
}
