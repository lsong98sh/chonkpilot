module github.com/chonkpilot/chonkpilot-cli

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-data v0.0.0
	github.com/chonkpilot/chonkpilot-lib v0.0.0
)

require (
	go.etcd.io/bbolt v1.4.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-data => ../data

replace github.com/chonkpilot/chonkpilot-lib => ../core
