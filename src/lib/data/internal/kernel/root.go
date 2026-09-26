// 数据根解析与三层库打开（12-数据层）——原 persist 的实例解析 helper 逐字下移：
//
//	prj    = 团队共享项目配置层（prj-config / prompt / prj-security）
//	prjusr = 会话/任务树/快照/个人运行态层（<usr 数据根>/data/<project-id>/chonkpilot.db）
//	usr    = 用户全局层（~/.chonkpilot/chonkpilot.db；UsrPath 可注入，测试隔离）
//
// 实例解析层序（三处读点同源）：
//
//	① 本进程实例视图（instance-register 自持；含"唯一实例回退"的既有语义）
//	② 调用方带入 Scope（跨进程/独立形态：本进程尚未登记该实例时**自登记**）
//	③ data 组件绑定表（同源另一视图）
package kernel

import (
	"errors"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// 解析失败口径（错误文案与下沉前一字不变——属既有可观察行为）。导出供信封层（persist）
// 复用同一实例（避免错误值分叉）。
var (
	// ErrInstanceNotRegistered 实例未登记（数据根解析不到）。
	ErrInstanceNotRegistered = errors.New("persist: instance not registered")
	// ErrInstanceIDRequired 未带 instance_id 且实例视图非唯一（无法回退）。
	ErrInstanceIDRequired = errors.New("persist: instance_id required (multiple instances)")
)

// dataUnregister 移除 data 组件绑定表条目（实例退出/超时回收用）。
func dataUnregister(instanceID string) { data.Unregister(instanceID) }

// OpenPrjLayer 打开实例的 prj 层（团队共享 / 项目预设配置；路径规则见 12-数据层 §6.2）。
func OpenPrjLayer(workDir, dataDir string) (*data.DB, func(), error) {
	return data.OpenSharedLayer(data.ProjectPath(workDir, dataDir), data.LayerPrj)
}

// OpenPrjUsrLayer 打开实例的 prjusr 层（个人运行态）：dataDir 非空 → 与 prj 同库（临时形态）；
// 否则 <usr 数据根>/data/<project-id>/chonkpilot.db（project-id 从 prj 库取，首次自动生成）。
func OpenPrjUsrLayer(dataDir string, prj *data.DB) (*data.DB, func(), error) {
	path := prj.Path()
	if dataDir == "" {
		id, err := data.EnsureProjectID(prj)
		if err != nil {
			return nil, nil, err
		}
		path = data.PrjUsrDBPath(id)
	}
	return data.OpenSharedLayer(path, data.LayerPrjUsr)
}

// UsrDB 打开 usr 层（用户全局配置；usrPath 为空回落 data.UserPath()）。
func (b *Base) UsrDB() (*data.DB, func(), error) {
	p := b.UsrPath
	if p == "" {
		p = data.UserPath()
	}
	return data.OpenSharedLayer(p, data.LayerUsr)
}

// CfgInstBind 解析实例数据根：① 本进程实例视图（instance_id 显式命中）② 调用方带入 Scope
// （自登记进 data 绑定表后返回）③ data 绑定表（同源另一视图）。解析不出 → 报"未登记"。
func (b *Base) CfgInstBind(instanceID string, scope facade.Scope) (workDir, dataDir string, err error) {
	if instanceID != "" {
		if info, ok := b.View.Lookup(instanceID); ok {
			return info.WorkDir, info.DataDir, nil
		}
	}
	if !scope.Empty() {
		data.Register(instanceID, scope.WorkDir, scope.DataDir)
		return scope.WorkDir, scope.DataDir, nil
	}
	if wd, dd, ok := data.BindOf(instanceID); ok {
		return wd, dd, nil
	}
	return "", "", ErrInstanceNotRegistered
}

// InstBindingFor 返回实例的 {work_dir, data_dir} 绑定（层序见文件头）；解析不出 → ok=false。
func (b *Base) InstBindingFor(instanceID string, scope facade.Scope) (string, string, bool) {
	if _, info, err := b.View.Resolve(instanceID); err == nil {
		return info.WorkDir, info.DataDir, true
	}
	if !scope.Empty() {
		data.Register(instanceID, scope.WorkDir, scope.DataDir)
		return scope.WorkDir, scope.DataDir, true
	}
	if wd, dd, ok := data.BindOf(instanceID); ok {
		data.Register(instanceID, wd, dd)
		return wd, dd, true
	}
	return "", "", false
}

// WorkDirFor 解析实例工作目录（**严格**：解析不出即报错）。knowledge / memory 两文件域用——
// 与既有 MQ 路径同口径（未登记实例 → 失败应答，不静默落错位置）。
func (b *Base) WorkDirFor(instanceID string, scope facade.Scope) (string, error) {
	if wd, _, ok := b.InstBindingFor(instanceID, scope); ok {
		return wd, nil
	}
	_, _, err := b.View.Resolve(instanceID)
	return "", err
}

// WorkDirLoose 解析实例工作目录（**宽松**：解析不出 → 空串）。scenario 域用——场景三级根在
// 实例未登记时仍可见 app + user 级（既有语义：`scenarioLevels` 容忍解析失败），只有 project
// 级需要 work_dir。
func (b *Base) WorkDirLoose(instanceID string, scope facade.Scope) string {
	wd, _, _ := b.InstBindingFor(instanceID, scope)
	return wd
}

// PrjFor 定位实例的 **prj 主库**（filelist 域：清单落项目级库，12-数据层）。
//
// 与 MQ 路径同口径：显式 instance_id 未登记即报错（**不引入**"唯一实例回退"——filelist 的
// 既有读方 vfts 插件恒带 instance_id）；门面路径另接受调用方带入 Scope / data 绑定表（自登记）。
func (b *Base) PrjFor(instanceID string, scope facade.Scope) (*data.DB, error) {
	if instanceID != "" {
		if info, ok := b.View.Lookup(instanceID); ok {
			return b.PrjByInst(instanceID, info)
		}
	}
	if !scope.Empty() {
		data.Register(instanceID, scope.WorkDir, scope.DataDir)
		return data.Prj(instanceID)
	}
	if wd, dd, ok := data.BindOf(instanceID); ok {
		data.Register(instanceID, wd, dd)
		return data.Prj(instanceID)
	}
	return nil, ErrInstanceNotRegistered
}

// PrjUsrFor 定位实例的 **prjusr 主库**（会话/轮次/消息/任务树/快照所在层，12-数据层）：
// ① 本进程实例视图（含"唯一实例回退"）② 调用方带入 Scope（独立形态 → 自登记）
// ③ data 组件绑定表（同源另一视图）。
func (b *Base) PrjUsrFor(instanceID string, scope facade.Scope) (*data.DB, error) {
	id, info, rerr := b.View.Resolve(instanceID)
	if rerr == nil {
		return b.PrjUsrByInst(id, info)
	}
	if !scope.Empty() {
		data.Register(instanceID, scope.WorkDir, scope.DataDir)
		return data.PrjUsr(instanceID)
	}
	if wd, dd, ok := data.BindOf(instanceID); ok {
		data.Register(instanceID, wd, dd)
		return data.PrjUsr(instanceID)
	}
	return nil, rerr
}

// PrjByInst 按实例绑定解析 prj 主库（绑定登记进 data 缓存）。
func (b *Base) PrjByInst(instanceID string, info Info) (*data.DB, error) {
	data.Register(instanceID, info.WorkDir, info.DataDir)
	return data.Prj(instanceID)
}

// PrjUsrByInst 按实例绑定解析 prjusr 主库（会话/任务树/快照/个人运行态）。
func (b *Base) PrjUsrByInst(instanceID string, info Info) (*data.DB, error) {
	data.Register(instanceID, info.WorkDir, info.DataDir)
	return data.PrjUsr(instanceID)
}

// OpenLayerDBs 以**可释放**句柄一次性打开实例的 prj + prjusr 两层，返回统一 release。
// 供只读合并取值（如 user-config 的可继承键）用完即释——避免走 data.Prj/PrjUsr 的
// 长驻连接缓存（其引用计数不释放，测试清理期会占用临时库文件）。路径解析与
// data.Prj/PrjUsr 一致（12-数据层 §6.2；规则单源 = 本文件的层打开器）。
// scope 供跨进程/独立形态带入实例数据根（MQ 路径留空 = 按本进程实例视图解析）。
func (b *Base) OpenLayerDBs(instanceID string, scope facade.Scope) (prj *data.DB, pudb *data.DB, release func(), err error) {
	workDir, dataDir, err := b.CfgInstBind(instanceID, scope)
	if err != nil {
		return nil, nil, nil, err
	}
	prj, relPrj, err := OpenPrjLayer(workDir, dataDir)
	if err != nil {
		return nil, nil, nil, err
	}
	pudb, relPu, err := OpenPrjUsrLayer(dataDir, prj)
	if err != nil {
		relPrj()
		return nil, nil, nil, err
	}
	return prj, pudb, func() { relPu(); relPrj() }, nil
}
