// 多实例访问 + 按 db 路径连接缓存（对齐 12-数据层）。
//
// server / 插件（compress 等）作为服务层服务于多个 instance，数据访问统一按
// instance_id 入口：路径解析（三级分根规则 + project-id 绑定）+ 连接缓存在本包内部
// 解决，调用方无感。
package data

import (
	"fmt"
	"sort"
	"sync"
)

// instBind 是 instance 绑定（登记自 instance-register 的 work_dir/data_dir）。
type instBind struct {
	WorkDir string
	DataDir string
	// PrjUsrPath 是该 instance 首次经 PrjUsr() 打开时**实际解析到的 prjusr 库文件路径**
	// （🆕 2026-10-09）。prjusr 为**常开**（引用不释放）→ 必须在 instance-exit 时按此路径
	// **释放其连接引用**（归零才关闭、释放 bbolt 文件锁），否则连接与锁会残留到进程退出
	// （多实例服务端尤其明显）。
	PrjUsrPath string
	// prjUsrDB / prjUsrRelease 是该 instance 持有的 prjusr 常开连接与其释放句柄
	// （🆕 2026-10-21，PRODSIM-1 L1）。同一 instance 对同一路径只持有**一份**引用；
	// 同 work_dir 的多个 instance 各持一份 → 任一实例退出只 refs--，归零才真正 Close，
	// 不会关闭兄弟窗口仍在用的连接。
	prjUsrDB      *DB
	prjUsrRelease func()
}

// sharedDB 是按路径缓存的连接项（引用计数）。
type sharedDB struct {
	db   *DB
	refs int
}

// 包级单例：instance 绑定表 + 路径缓存（并发安全）。
var (
	storeMu   sync.Mutex
	instances = make(map[string]instBind)
	shared    = make(map[string]*sharedDB)
	// openLocks 是按路径的单飞锁（每路径一把）：bbolt.Open 可能阻塞至超时，故打开动作
	// **不持 storeMu**，只持该路径自己的锁（同路径并发只打开一次，不同路径互不阻塞）。
	// refs 记在册引用（持有者 + 等待者）；归零后回收条目，避免按路径只增不清（A-12）——
	// 计数保证「无人持有/等待」时才删除，不会与在飞打开动作错配出两把不同的锁。
	openLocks = make(map[string]*openLock)
)

// openLock 是路径级单飞锁 + 在册引用计数（refs 归零才可从 openLocks 移除）。
type openLock struct {
	mu   sync.Mutex
	refs int
}

// Register 登记 instance 绑定（幂等；重复登记刷新 work_dir/data_dir）。
// 登记源：server 收 instance-register 时调用（统一）；独立服务（compress 等）可凭事件载荷自登记。
//
// **空键守卫（G-41-a）**：instanceID 为空 → **拒绝登记**（返回明确错误、不入 map）。
// 空 instanceID 属调用方缺陷（最早处暴露）：登记空键会使后续 `bindOf("")`/`BindOf("")`
// 命中该条 → 空 instance 请求被静默解析到某实例数据根（多实例下跨实例串库）。
func Register(instanceID, workDir, dataDir string) error {
	if instanceID == "" {
		return fmt.Errorf("data: 拒绝登记空 instanceID（work_dir=%q）", workDir)
	}
	storeMu.Lock()
	old, existed := instances[instanceID]
	if existed && old.WorkDir == workDir && old.DataDir == dataDir {
		// 幂等：同参数重复登记（bridge + claim 双注册）**保留**已持有的 prjusr 绑定 ——
		// 整体覆盖会清空 PrjUsrPath / prjUsrRelease，导致 instance-exit 不释放连接
		// （PRODSIM-1 L1 缺陷③：prjusr 常开句柄永不释放）。
		storeMu.Unlock()
		return nil
	}
	stale := old.prjUsrRelease
	instances[instanceID] = instBind{WorkDir: workDir, DataDir: dataDir}
	storeMu.Unlock()
	if stale != nil {
		stale() // 数据根变更：归还旧 prjusr 引用（解析路径可能已变）
	}
	return nil
}

// InstancesByWorkDir 返回 work_dir 等于 workDir 的全部**在册** instance id（字典序，稳定）。
//
// 用途：项目级变更（如 `data-prj-config-refresh`）需通知**同项目（同 work_dir）的全部在册
// 实例** —— 多数 MQ 按 instance 过滤，故按 work_dir 匹配后逐个投递（G-41-b）。
// workDir 为空 → 返回 nil（不匹配空工作目录，避免误命中批量未登记项）。
func InstancesByWorkDir(workDir string) []string {
	if workDir == "" {
		return nil
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	var ids []string
	for id, b := range instances {
		if b.WorkDir == workDir {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// DataDirByWorkDir 返回同 work_dir 已登记实例的**非空** data_dir（ok=false = 无可复用数据根）。
//
// 用途：调用方只带 work_dir（filesys 变更入队 / 插件桥队列控制帧等）时补齐 data_dir ——
// 否则会在 work_dir 下另建 prj 库，破坏 `--data-dir` 分离形态的「workdir 零污染」约束
// （12-数据层 / 24 §3）。同 work_dir 多个非空绑定取字典序首个（幂等、稳定）。
func DataDirByWorkDir(workDir string) (string, bool) {
	if workDir == "" {
		return "", false
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	ids := make([]string, 0, len(instances))
	for id, b := range instances {
		if b.WorkDir == workDir && b.DataDir != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return "", false
	}
	return instances[ids[0]].DataDir, true
}

// Unregister 移除 instance 绑定（instance-exit / 超时清理时调用）。
// **并关闭该 instance 的 prjusr 常开连接**（🆕 2026-10-09）：prjusr 引用不释放 → 不解绑时
// 其 bbolt 连接与文件锁会残留（D-45 同类问题：长持会锁死外部进程）。按首次打开的路径关闭缓存条目。
//
// **按实例引用计数（🆕 2026-10-10）**：`shared` 按**库路径**缓存，而 prjusr 路径由 project-id
// 决定 —— 同 work_dir 的多实例会解析到**同一 prjusr 库路径、共享同一 `*DB`**。若一个实例退出
// 即直接 Close，会**关闭他人仍在用的连接**（后续请求报错）。故此处先移除本实例，再检查**是否
// 仍有其它在册实例持有同一 prjusr 路径**：仅当无任何其它持有者时才真正 Close（= 实例级引用
// 计数归零）。单实例场景仍能在退出时关闭连接（D-45 诉求）；幂等（重复 Unregister 空操作）。
func Unregister(instanceID string) {
	storeMu.Lock()
	bind, ok := instances[instanceID]
	delete(instances, instanceID)
	storeMu.Unlock()
	if !ok || bind.prjUsrRelease == nil {
		return
	}
	// 归还**本实例**持有的 prjusr 引用：refs 归零才真正 Close —— 同 work_dir 的兄弟窗口
	// 仍持有时只减计数、不关闭（PRODSIM-1 L1 缺陷①：原实现无其它在册实例即强关，会让
	// 兄弟窗口后续请求拿到已关闭连接）。
	bind.prjUsrRelease()
}

// Reset 清空 instance 绑定与连接缓存（测试用；关闭全部缓存连接）。
func Reset() {
	storeMu.Lock()
	defer storeMu.Unlock()
	for _, s := range shared {
		_ = s.db.Close()
	}
	instances = make(map[string]instBind)
	shared = make(map[string]*sharedDB)
}

// bindOf 取 instance 绑定。
func bindOf(instanceID string) (instBind, error) {
	storeMu.Lock()
	bind, ok := instances[instanceID]
	storeMu.Unlock()
	if !ok {
		return instBind{}, fmt.Errorf("data: instance %q not registered", instanceID)
	}
	return bind, nil
}

// BindOf 按 instance 取数据根绑定（work_dir/data_dir）；未登记 → ok=false。
//
// 供**无自持实例视图**的服务面（如 data 门面的 inline 绑定：跨进程/独立形态凭事件载荷
// 自登记，或读本表）解析数据根——与 Register/Prj/PrjUsr 同一份事实（12-数据层）。
func BindOf(instanceID string) (workDir, dataDir string, ok bool) {
	storeMu.Lock()
	bind, ok := instances[instanceID]
	storeMu.Unlock()
	if !ok {
		return "", "", false
	}
	return bind.WorkDir, bind.DataDir, true
}

// Prj 返回该 instance 的 prj 主库连接（团队共享配置；含 project-id）。
// 路径：dataDir 非空（CLI 临时形态）→ <dataDir>/chonkpilot.db；否则 <workDir>/.chonkpilot/chonkpilot.db。
//
// **短开语义（D-45）**：prj 层按调用短开（连接缓存引用计数，release 后归零即 Close）——
// 使 GUI 与 CLI 可并发打开同一项目 prj 库（bbolt 单文件排他锁：长持连接会把对方锁在外面）。
// 调用方必须配对调用 release；高频读路径由 config 门面的值缓存吸收开销（12-数据层 §5.4）。
func Prj(instanceID string) (*DB, func(), error) {
	bind, err := bindOf(instanceID)
	if err != nil {
		return nil, nil, err
	}
	return OpenSharedLayer(ProjectPath(bind.WorkDir, bind.DataDir), LayerPrj)
}

// PrjUsr 返回该 instance 的 prjusr 主库连接（会话/任务树/快照/个人运行态）。
//
//	dataDir 非空（CLI 临时形态）→ <dataDir>/chonkpilot.db（与 prj 同文件；会话写在临时目录，退出即弃）
//	否则 → <workDir>/.chonkpilot/users/<OS 用户名>/chonkpilot.db（数据随项目目录走，见 49 DATAROOT-1）
//
// **常开语义（D-45 拍板）**：prjusr 层是会话/任务树运行态（高频写、独占本机数据根），
// 保持进程内常开（连接缓存引用不释放）；仅 prj/usr 改短开。
func PrjUsr(instanceID string) (*DB, error) {
	// 已在册持有 → 直接返回：避免每次调用都 refs++ 再归还的额外锁开销，也保证 refs
	// 稳定等于「持有该连接的实例数」（原始实现每次调用 refs++ 且从不释放 → 计数无界增长）。
	if db, ok := heldPrjUsr(instanceID); ok {
		return db, nil
	}
	bind, err := bindOf(instanceID)
	if err != nil {
		return nil, err
	}
	path := PrjUsrDBPath(bind.WorkDir, bind.DataDir)
	db, release, err := OpenSharedLayer(path, LayerPrjUsr)
	if err != nil {
		return nil, err
	}
	bindPrjUsr(instanceID, path, db, release)
	return db, nil
}

// heldPrjUsr 返回该 instance 已持有的 prjusr 连接（ok=false = 未持有）。
func heldPrjUsr(instanceID string) (*DB, bool) {
	storeMu.Lock()
	defer storeMu.Unlock()
	b, ok := instances[instanceID]
	if !ok || b.prjUsrDB == nil {
		return nil, false
	}
	return b.prjUsrDB, true
}

// bindPrjUsr 把 prjusr 常开连接绑定到 instance —— **每 instance 每路径只持一份引用**。
// 并发下另一协程已绑定同路径 → 归还本次多余引用；路径变更（如 project-id 被改）→ 归还旧引用
// 后改绑新连接；实例已退出 → 直接归还（不把已注销实例塞回表）。
func bindPrjUsr(instanceID, path string, db *DB, release func()) {
	storeMu.Lock()
	b, ok := instances[instanceID]
	if !ok {
		storeMu.Unlock()
		release()
		return
	}
	if b.prjUsrRelease != nil && b.PrjUsrPath == path {
		storeMu.Unlock()
		release() // 已持有同路径引用：归还本次多余引用
		return
	}
	stale := b.prjUsrRelease // 可能为 nil（首次绑定）
	b.PrjUsrPath = path
	b.prjUsrDB = db
	b.prjUsrRelease = release
	instances[instanceID] = b
	storeMu.Unlock()
	if stale != nil {
		stale() // 路径变更：归还旧路径引用
	}
}

// Usr 返回 usr 层连接（全局单例：~/.chonkpilot/chonkpilot.db，跨项目）。
// **短开语义（D-45）**：同 Prj——release 归零即 Close，避免长持锁死 GUI 侧同名库。
func Usr() (*DB, func(), error) {
	return OpenSharedLayer(UserPath(), LayerUsr)
}

// OpenShared 按 db 文件路径缓存打开连接 + 引用计数。
//
// 缓存键 = **db 文件最终路径**（非 data_dir）：不同 data_dir/不同项目可能解析到同一文件，
// 按路径缓存保证同文件单连接、不重复 Open（bbolt 单文件独占锁）。
//
// 返回 Release：计数归零才真正 Close（配对使用；Prj/Usr 为短开必须配对 release，
// PrjUsr 为常开、调用方无需管生命周期）。
func OpenShared(path string) (*DB, func(), error) {
	return OpenSharedLayer(path, "")
}

// OpenSharedLayer 同 OpenShared，但以指定层打开（层标识写入 DB 元信息，供诊断）。
//
// 锁范围（性能）：storeMu 只覆盖 map 查/占位；`bbolt.Open`（含 3s 锁等待）在**锁外**执行，
// 并以 per-path 单飞锁保证同路径并发只打开一次 —— 某层被外部进程占锁时，
// 不会阻塞其他路径的打开/解析（原实现全程持 storeMu，会把整个进程内所有 instance 的
// DB 打开串行化并阻塞至超时）。
func OpenSharedLayer(path string, layer Layer) (*DB, func(), error) {
	// 快路径：已缓存 → 仅计数（lock-内极短）。
	storeMu.Lock()
	if s, ok := shared[path]; ok {
		s.refs++
		storeMu.Unlock()
		return s.db, releaseOnce(path, s), nil
	}
	// 取该路径的单飞锁（不存在则建）并计入引用；refs>0 期间条目不会被回收。
	pl := openLocks[path]
	if pl == nil {
		pl = &openLock{}
		openLocks[path] = pl
	}
	pl.refs++
	storeMu.Unlock()

	// 锁外打开（同路径串行，不阻塞其他路径）；收尾释放锁并归还引用（归零则回收条目）。
	pl.mu.Lock()
	defer func() {
		pl.mu.Unlock()
		storeMu.Lock()
		pl.refs--
		if pl.refs <= 0 {
			delete(openLocks, path)
		}
		storeMu.Unlock()
	}()
	// 复查：等待期间他协程可能已建好缓存。
	storeMu.Lock()
	if s, ok := shared[path]; ok {
		s.refs++
		storeMu.Unlock()
		return s.db, releaseOnce(path, s), nil
	}
	storeMu.Unlock()

	db, err := OpenLayer(path, layer)
	if err != nil {
		return nil, nil, err
	}
	s := &sharedDB{db: db, refs: 1}
	storeMu.Lock()
	shared[path] = s
	storeMu.Unlock()
	return db, releaseOnce(path, s), nil
}

// releaseOnce 返回幂等的 Release 闭包（计数归零 → Close + 移除缓存）。
func releaseOnce(path string, s *sharedDB) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			storeMu.Lock()
			defer storeMu.Unlock()
			s.refs--
			if s.refs <= 0 {
				_ = s.db.Close()
				// 只删「本代」条目：若该路径在此期间已被重新打开（新一代 s2 入缓存），
				// 无条件 delete 会误删新一代 → 下次访问再次 Open 撞 bbolt 同进程互斥并泄漏句柄
				// （PRODSIM-1 L1 缺陷②）。
				if shared[path] == s {
					delete(shared, path)
				}
				// 单飞锁条目由 OpenSharedLayer 内的 refs 计数自行回收（A-12），此处不删，
				// 以免与在飞的打开动作错配出两把不同的锁。
			}
		})
	}
}
