module github.com/chonkpilot/chonkpilot-genmsg

go 1.26.0

// TOPO-1锛?026-10-29锛夛細骞冲彴鍩哄骇锛坈ore + 绐楀彛灞傦級宸茶縼鑷?framework/host銆俙r
replace github.com/lsong98sh/chonkpilot-framework/host => ../../../../chonkpilot-framework/host/src

replace github.com/chonkpilot/chonkpilot-workspace => ../../lib/workspace

replace github.com/lsong98sh/chonkpilot-agent => ../../../../chonkpilot-agent

replace github.com/lsong98sh/chonkpilot-agent/chat => ../../../../chonkpilot-agent/chat/src

replace github.com/lsong98sh/chonkpilot-agent/gateway => ../../../../chonkpilot-agent/gateway/src

replace github.com/lsong98sh/chonkpilot-agent/router => ../../../../chonkpilot-agent/router/src

replace github.com/lsong98sh/chonkpilot-agent/session => ../../../../chonkpilot-agent/session/src

replace github.com/lsong98sh/chonkpilot-agent/types => ../../../../chonkpilot-agent/types/src
