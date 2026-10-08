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
)

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
	defer storeMu.Unlock()
	instances[instanceID] = instBind{WorkDir: workDir, DataDir: dataDir}
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

// Unregister 移除 instance 绑定（instance-exit / 超时清理时调用）。
func Unregister(instanceID string) {
	storeMu.Lock()
	defer storeMu.Unlock()
	delete(instances, instanceID)
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
//	否则 → ~/.chonkpilot/data/<project-id>/chonkpilot.db
//	       （project-id 取自 prj 库 config，首次自动生成，见 12-数据层）
//
// **常开语义（D-45 拍板）**：prjusr 层是会话/任务树运行态（高频写、独占本机数据根），
// 保持进程内常开（连接缓存引用不释放）；仅 prj/usr 改短开。prj 短开取 project-id 后立即 release。
func PrjUsr(instanceID string) (*DB, error) {
	bind, err := bindOf(instanceID)
	if err != nil {
		return nil, err
	}
	if bind.DataDir != "" {
		db, _, err := OpenSharedLayer(ProjectPath(bind.WorkDir, bind.DataDir), LayerPrjUsr)
		return db, err
	}
	prj, release, err := Prj(instanceID)
	if err != nil {
		return nil, err
	}
	defer release() // 短开：取 project-id 后立即释放（D-45）
	id, err := EnsureProjectID(prj)
	if err != nil {
		return nil, err
	}
	db, _, err := OpenSharedLayer(PrjUsrDBPath(id), LayerPrjUsr)
	return db, err
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
func OpenSharedLayer(path string, layer Layer) (*DB, func(), error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	if s, ok := shared[path]; ok {
		s.refs++
		return s.db, releaseOnce(path, s), nil
	}
	db, err := OpenLayer(path, layer)
	if err != nil {
		return nil, nil, err
	}
	s := &sharedDB{db: db, refs: 1}
	shared[path] = s
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
				delete(shared, path)
			}
		})
	}
}
