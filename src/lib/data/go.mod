module github.com/chonkpilot/chonkpilot-data

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-ignore v0.0.0
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	go.etcd.io/bbolt v1.4.2
)

require (
	golang.org/x/crypto v0.55.0
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-ignore => ../ignore

replace github.com/chonkpilot/chonkpilot-lib => ../core
