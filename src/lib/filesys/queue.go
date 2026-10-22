// 待索引队列写入口（P2）：filesys 在广播 filesys.changed 的同时，把每条文件变更
// 写入宿主注入的待索引队列（消费方 = resident indexer）。本模块**零 data 依赖**，
// 队列实现由宿主经 Options.IndexQueue 注入；nil = 不入队。
package filesys

// IndexChange 是一条待索引的文件变更。
type IndexChange struct {
	WorkDir   string
	Path      string
	Operation string // create|write|remove|rename
}

// IndexQueue 是待索引队列写入口（由宿主注入；nil = 不入队）。
type IndexQueue interface {
	Append(IndexChange) error
}

// IndexQueueFunc 便于宿主用闭包注入。
type IndexQueueFunc func(IndexChange) error

// Append 实现 IndexQueue（闭包适配）。
func (f IndexQueueFunc) Append(c IndexChange) error { return f(c) }

// Options 是 Filesys 的可选依赖。
type Options struct {
	// IndexQueue 待索引队列写入口；nil = 不入队（filesys.changed 广播不受影响）。
	IndexQueue IndexQueue
}
