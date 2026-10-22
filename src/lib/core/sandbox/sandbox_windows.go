//go:build windows

package sandbox

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// lowIntegritySID 是 Low Integrity Level 的强制标签 SID（S-1-16-4096）。
	lowIntegritySID = "S-1-16-4096"
	// seGroupIntegrity 是 TOKEN_MANDATORY_LABEL.Attributes 的 SE_GROUP_INTEGRITY 标志。
	seGroupIntegrity = 0x00000020
)

// tokenMandatoryLabel 对应 Win32 TOKEN_MANDATORY_LABEL：
// SID 必须紧跟在结构体之后连续存放（GetTokenInformation/SetTokenInformation 按长度读取）。
type tokenMandatoryLabel struct {
	Label windows.SIDAndAttributes
}

// Start 以 Low Integrity Level 令牌启动 cmd，并在启动后把进程放进 Job Object。
//
// 全程不修改任何 DACL。调用方须先对允许写目录调用 [EnsureLowIL]，
// 否则子进程对它们也只能读不能写（这是 fail-closed 行为，不是 bug）。
//
// 返回的 cleanup 幂等：关闭 Job（KILL_ON_JOB_CLOSE 连带杀进程树）并释放令牌句柄。
func Start(cmd *exec.Cmd, spec Spec) (cleanup func(), err error) {
	tok, err := duplicateLowILToken()
	if err != nil {
		return nil, err
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = syscall.Token(tok)

	job, err := createJob(spec.Limits)
	if err != nil {
		_ = tok.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		_ = tok.Close()
		return nil, fmt.Errorf("sandbox: 启动子进程失败: %w", err)
	}

	// 入 Job 需要 PROCESS_SET_QUOTA | PROCESS_TERMINATE。
	ph, oerr := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if oerr != nil {
		_ = cmd.Process.Kill()
		windows.CloseHandle(job)
		_ = tok.Close()
		return nil, fmt.Errorf("sandbox: 打开子进程句柄失败: %w", oerr)
	}
	jobAssigned := true
	if aerr := windows.AssignProcessToJobObject(job, ph); aerr != nil {
		// 子进程已在别的 Job 中（如 IDE 的 Job）→ 无法再入 Job。
		// 降级为「只保留 Low IL」，不因此判失败（写隔离仍生效）。
		jobAssigned = false
		windows.CloseHandle(job)
		job = 0
	}
	windows.CloseHandle(ph)

	return func() {
		if jobAssigned && job != 0 {
			windows.CloseHandle(job) // KILL_ON_JOB_CLOSE → 杀进程树
		}
		_ = tok.Close()
	}, nil
}

// Prepare 只把 Low IL 令牌挂到 cmd 上（不 Start、不建 Job），
// 供由第三方负责 Start 的场景（如 MCP SDK 的 CommandTransport）使用。
//
// 令牌句柄不显式关闭：它随本进程退出由 OS 回收，避免与第三方 Start 时机竞争。
// 每个 (server, workdir) 槽最多泄漏一个句柄，规模可控。
func Prepare(cmd *exec.Cmd, spec Spec) error {
	tok, err := duplicateLowILToken()
	if err != nil {
		return err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = syscall.Token(tok)
	return nil
}

// EnsureLowIL 给目录打 Low IL 标签（`(OI)(CI)L`，递归继承到新建子项），使 Low IL 子进程可写。
//
// 幂等：重复调用不报错。失败通常意味着当前用户对该目录没有 WRITE_OWNER / SeRelabelPrivilege，
// 调用方应据此 fail-closed（不启动沙箱进程）或提示用户手工配置。
func EnsureLowIL(dir string) error {
	out, err := exec.Command("icacls", dir, "/setintegritylevel", "(OI)(CI)L").CombinedOutput()
	if err != nil {
		return fmt.Errorf("sandbox: 给 %q 打 Low IL 标签失败: %w（%s）", dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// duplicateLowILToken 复制当前进程令牌为 primary token 并设为 Low IL。
//
// 刻意**不使用 restricting SIDs**（如 WRITE_RESTRICTED）：那会触发「双重允许」检查，
// 导致子进程连自己的解释器/DLL 都读不到（实测 python 在用户目录直接 Access denied）。
func duplicateLowILToken() (windows.Token, error) {
	var cur windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT, &cur); err != nil {
		return 0, fmt.Errorf("sandbox: OpenProcessToken: %w", err)
	}
	defer cur.Close()

	var primary windows.Token
	if err := windows.DuplicateTokenEx(cur, windows.MAXIMUM_ALLOWED, nil,
		windows.SecurityImpersonation, windows.TokenPrimary, &primary); err != nil {
		return 0, fmt.Errorf("sandbox: DuplicateTokenEx: %w", err)
	}

	lowSID, err := windows.StringToSid(lowIntegritySID)
	if err != nil {
		_ = primary.Close()
		return 0, fmt.Errorf("sandbox: 解析 Low IL SID %q: %w", lowIntegritySID, err)
	}

	hdrLen := int(unsafe.Sizeof(tokenMandatoryLabel{}))
	sidLen := int(windows.GetLengthSid(lowSID))
	buf := make([]byte, hdrLen+sidLen)

	tml := (*tokenMandatoryLabel)(unsafe.Pointer(&buf[0]))
	tml.Label.Sid = (*windows.SID)(unsafe.Pointer(&buf[hdrLen]))
	tml.Label.Attributes = seGroupIntegrity
	copy(buf[hdrLen:], unsafe.Slice((*byte)(unsafe.Pointer(lowSID)), sidLen))

	if err := windows.SetTokenInformation(primary, windows.TokenIntegrityLevel, &buf[0], uint32(len(buf))); err != nil {
		_ = primary.Close()
		return 0, fmt.Errorf("sandbox: SetTokenInformation(TokenIntegrityLevel=Low): %w", err)
	}
	return primary, nil
}

// createJob 创建 Job Object 并设置资源上限 + KILL_ON_JOB_CLOSE。
func createJob(lim Limits) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("sandbox: CreateJobObject: %w", err)
	}

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if lim.MaxProcesses > 0 {
		info.BasicLimitInformation.ActiveProcessLimit = uint32(lim.MaxProcesses)
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
	}
	if lim.MaxMemoryBytes > 0 {
		info.JobMemoryLimit = uintptr(lim.MaxMemoryBytes)
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
	}

	if _, serr := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); serr != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("sandbox: SetInformationJobObject: %w", serr)
	}
	return job, nil
}
