module github.com/chonkpilot/chonkpilot-test/chonkpilot-cli/unittest

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-cli v0.0.0
	github.com/chonkpilot/chonkpilot-data v0.0.0
	github.com/chonkpilot/chonkpilot-lib v0.0.0
)

require (
	go.etcd.io/bbolt v1.4.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-cli => ../../../lib/cli

replace github.com/chonkpilot/chonkpilot-data => ../../../lib/data

replace github.com/chonkpilot/chonkpilot-lib => ../../../lib/core
