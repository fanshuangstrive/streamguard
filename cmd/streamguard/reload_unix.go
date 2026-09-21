//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyReloadSignal 在 Unix 平台监听 SIGHUP 作为配置热重载信号。
//
// 用法：修改配置文件后执行 `kill -HUP <pid>`，进程会重新加载配置并重启服务，
// 无需中断进程（服务中断约几十毫秒）。
func notifyReloadSignal(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGHUP)
}
