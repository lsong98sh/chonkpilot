module github.com/chonkpilot/chonkpilot-test/chonkpilot-filesys/unittest

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-filesys v0.0.0
	github.com/chonkpilot/chonkpilot-lib v0.0.0
)

require (
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-filesys => ../../../lib/filesys

replace github.com/chonkpilot/chonkpilot-lib => ../../../lib/core
