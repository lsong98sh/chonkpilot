package pluginsdk

import "net"

// listenLoopback 监听 127.0.0.1 的随机端口（仅本机可达）。
func listenLoopback() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}
