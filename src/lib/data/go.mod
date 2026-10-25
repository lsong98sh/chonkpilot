module github.com/chonkpilot/chonkpilot-data

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-ignore v0.0.0
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	github.com/lsong98sh/chonkpilot-session v0.1.0
	go.etcd.io/bbolt v1.4.2
)

require github.com/lsong98sh/chonkpilot-chat v0.1.0 // indirect

require (
	github.com/lsong98sh/chonkpilot-plugin-framework v0.1.3 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-ignore => ../ignore

replace github.com/chonkpilot/chonkpilot-lib => ../core
