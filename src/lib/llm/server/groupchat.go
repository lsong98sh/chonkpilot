// 群聊项目级配置读取（SUBCHAT-3 阶段 E；设计见 `docs/agent-chat/群聊会话设计方案.md`
// §10 C8 · §7 ③ · D49）。
//
// 本文件只落**配置读取能力**（唯一硬闸「最大轮次」的取值）；**到顶处置**（硬停 + 主 agent
// 终审 + 用户可「继续」续跑，§7 ③ / D50）属**阶段 F**（调度器），不在此。
//
// 键与语义（登记见 [64-配置项一览 §4.1](../../../docs/spec/60-reference/64-配置项一览.md)）：
//   - 键 = `group_chat.max_rounds`（**项目级 prj**，非系统级；十进制整数字符串）；
//   - 默认 = 20；可选区间 = 20–40；
//   - **区间外 / 缺失 / 非数字 → 回落默认 20**（不 clamp、不取任意值 —— 误配不静默放大成
//     "跑飞"风险；与同族「缺失/非数字 → 回落默认」口径一致，见 assemble.go 的三个项目级键）。
//
// 读序 = 门面 `ConfigKVGet`（prjusr → prj → usr），与其余项目级键同源（`prjConfigValues`）。
package server

import (
	"strconv"
	"strings"
)

const (
	// groupChatMaxRoundsKey 群聊最大轮次的项目级配置键（§10 C8）。
	groupChatMaxRoundsKey = "group_chat.max_rounds"
	// defaultGroupChatMaxRounds 缺省最大轮次（D49：默认 20）。
	defaultGroupChatMaxRounds = 20
	// minGroupChatMaxRounds / maxGroupChatMaxRounds 可选区间（D49：~20–40；区间外视为非法 → 回落默认）。
	minGroupChatMaxRounds = 20
	maxGroupChatMaxRounds = 40
)

// groupChatMaxRoundsOf 从**已读** prj 配置值表折算群聊最大轮次（纯函数，不再读配置）。
func groupChatMaxRoundsOf(vals map[string]string) int {
	n, err := strconv.Atoi(strings.TrimSpace(vals[groupChatMaxRoundsKey]))
	if err != nil || n < minGroupChatMaxRounds || n > maxGroupChatMaxRounds {
		return defaultGroupChatMaxRounds
	}
	return n
}

// loadGroupChatMaxRounds 读项目级 `group_chat.max_rounds`（缺失 / 非数字 / 区间外 → 默认 20）。
//
// 每次现读（群聊启动 / 续跑前调用即可热生效，与其余项目级键的「保存即生效」口径一致）。
func (s *Server) loadGroupChatMaxRounds(instanceID string) int {
	return groupChatMaxRoundsOf(s.prjConfigValues(instanceID, groupChatMaxRoundsKey))
}
