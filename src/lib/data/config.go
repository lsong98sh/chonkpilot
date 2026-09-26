// 三层便捷门面（对齐 12-数据层）：Config.OpenConfig 三层全开 + 分层解析器。
package data

// Config 持有三级 chonkpilot.db（usr / prj / prjusr）+ 分层解析器。
type Config struct {
	usr    *DB
	prj    *DB
	prjUsr *DB

	// release* 是各层的 OpenShared 释放句柄（Close 时配对释放）。
	// 同进程多组件共用同一数据层时 OpenShared 按路径去重返回同一连接，
	// 避免 bbolt 同文件二次 Open 互斥卡死（12-数据层）。
	releaseUsr    func()
	releasePrj    func()
	releasePrjUsr func()

	res *Resolver
}

// OpenConfig 打开三层（v6 路径规则，见 12-数据层）：
//
//	usr    = ~/.chonkpilot/chonkpilot.db
//	prj    = <workDir>/.chonkpilot/chonkpilot.db（dataDir 非空 → <dataDir>/chonkpilot.db）
//	prjusr = ~/.chonkpilot/data/<project-id>/chonkpilot.db
//	         （dataDir 非空 → 同 prj 库；project-id 取自 prj 库，首次自动生成）
//
// system 是系统级兜底取值函数（资源/常量），可为 nil。
func OpenConfig(workDir, dataDir string, system SystemDefault) (*Config, error) {
	usr, releaseUsr, err := OpenSharedLayer(UserPath(), LayerUsr)
	if err != nil {
		return nil, err
	}
	prjPath := ProjectPath(workDir, dataDir)
	prj, releasePrj, err := OpenSharedLayer(prjPath, LayerPrj)
	if err != nil {
		releaseUsr()
		return nil, err
	}
	prjUsrPath := prjPath // CLI 临时形态：与 prj 同库
	if dataDir == "" {
		id, err := EnsureProjectID(prj)
		if err != nil {
			releasePrj()
			releaseUsr()
			return nil, err
		}
		prjUsrPath = PrjUsrDBPath(id)
	}
	prjUsr, releasePrjUsr, err := OpenSharedLayer(prjUsrPath, LayerPrjUsr)
	if err != nil {
		releasePrj()
		releaseUsr()
		return nil, err
	}
	return &Config{
		usr: usr, prj: prj, prjUsr: prjUsr,
		releaseUsr: releaseUsr, releasePrj: releasePrj, releasePrjUsr: releasePrjUsr,
		res: NewResolver(prjUsr, prj, usr, system),
	}, nil
}

// Usr 返回 usr 层（用户配置/偏好/凭据）。
func (c *Config) Usr() *DB { return c.usr }

// Prj 返回 prj 层（团队共享项目配置）。
func (c *Config) Prj() *DB { return c.prj }

// PrjUsr 返回 prjusr 层（会话/任务树/个人运行态）。
func (c *Config) PrjUsr() *DB { return c.prjUsr }

// Resolver 返回分层解析器（读 prjusr → prj → usr → 系统；写指定层）。
func (c *Config) Resolver() *Resolver { return c.res }

// Close 关闭全部三层（OpenShared 引用计数归零才真正 Close）。
func (c *Config) Close() {
	if c.releasePrjUsr != nil {
		c.releasePrjUsr()
	}
	if c.releasePrj != nil {
		c.releasePrj()
	}
	if c.releaseUsr != nil {
		c.releaseUsr()
	}
}
