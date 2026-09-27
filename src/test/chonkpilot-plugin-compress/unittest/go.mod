module github.com/chonkpilot/chonkpilot-test/chonkpilot-plugin-compress/unittest

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-data v0.0.0
	github.com/chonkpilot/chonkpilot-ignore v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	github.com/chonkpilot/chonkpilot-plugin v0.0.0
	github.com/chonkpilot/chonkpilot-plugin-compress v0.0.0
)

require (
	go.etcd.io/bbolt v1.4.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-data => ../../../lib/data

replace github.com/chonkpilot/chonkpilot-ignore => ../../../lib/ignore

replace github.com/chonkpilot/chonkpilot-lib => ../../../lib/core

replace github.com/chonkpilot/chonkpilot-plugin => ../../../plugins/plugin

replace github.com/chonkpilot/chonkpilot-plugin-compress => ../../../plugins/plugin-compress
