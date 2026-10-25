module github.com/chonkpilot/chonkpilot-filesys

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	github.com/sgtdi/fswatcher v1.3.0
)

require (
	github.com/lsong98sh/chonkpilot-plugin-framework v0.1.3 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-lib => ../core
