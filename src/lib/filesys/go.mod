module github.com/chonkpilot/chonkpilot-filesys

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	github.com/fsnotify/fsnotify v1.9.0
)

require golang.org/x/sys v0.47.0 // indirect

replace github.com/chonkpilot/chonkpilot-lib => ../core
