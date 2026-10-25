module github.com/chonkpilot/chonkpilot-plugin-compress

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-data v0.0.0
	github.com/chonkpilot/chonkpilot-lib v0.0.0
)

require (
	github.com/chonkpilot/chonkpilot-ignore v0.0.0 // indirect
	github.com/lsong98sh/chonkpilot-chat v0.1.0 // indirect
	github.com/lsong98sh/chonkpilot-session v0.1.0 // indirect
	go.etcd.io/bbolt v1.4.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-data => ../../lib/data

replace github.com/chonkpilot/chonkpilot-ignore => ../../lib/ignore

replace github.com/chonkpilot/chonkpilot-lib => ../../lib/core

require github.com/lsong98sh/chonkpilot-plugin-framework v0.1.2
