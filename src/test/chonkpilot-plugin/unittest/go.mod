module github.com/chonkpilot/chonkpilot-test/chonkpilot-plugin/unittest

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	github.com/chonkpilot/chonkpilot-plugin v0.0.0
)

replace github.com/chonkpilot/chonkpilot-lib => ../../../lib/core

replace github.com/chonkpilot/chonkpilot-plugin => ../../../plugins/plugin
