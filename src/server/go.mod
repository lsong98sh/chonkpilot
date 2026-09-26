module github.com/chonkpilot/chonkpilot-server

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-data v0.0.0
	github.com/chonkpilot/chonkpilot-filesys v0.0.0
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	github.com/chonkpilot/chonkpilot-llm v0.0.0
	github.com/chonkpilot/chonkpilot-mcp-gateway v0.0.0
	github.com/chonkpilot/chonkpilot-mcp-server v0.0.0
	github.com/chonkpilot/chonkpilot-plugin v0.0.0
	github.com/chonkpilot/chonkpilot-plugin-codegraph v0.0.0-00010101000000-000000000000
	github.com/chonkpilot/chonkpilot-plugin-compress v0.0.0
	github.com/chonkpilot/chonkpilot-plugin-history v0.0.0
	github.com/chonkpilot/chonkpilot-plugin-memory v0.0.0
	github.com/chonkpilot/chonkpilot-plugin-vfts v0.0.0
	github.com/chonkpilot/chonkpilot-router v0.0.0
	github.com/chonkpilot/chonkpilot-task v0.0.0
	github.com/fsnotify/fsnotify v1.9.0
	github.com/modelcontextprotocol/go-sdk v1.7.0
	golang.org/x/sys v0.47.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.etcd.io/bbolt v1.4.2 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/chonkpilot/chonkpilot-data => ../lib/data

replace github.com/chonkpilot/chonkpilot-filesys => ../lib/filesys

replace github.com/chonkpilot/chonkpilot-lib => ../lib/core

replace github.com/chonkpilot/chonkpilot-llm => ../lib/llm

replace github.com/chonkpilot/chonkpilot-mcp-gateway => ../lib/gateway

replace github.com/chonkpilot/chonkpilot-mcp-server => ../lib/mcp-server

replace github.com/chonkpilot/chonkpilot-plugin => ../plugins/plugin

replace github.com/chonkpilot/chonkpilot-plugin-codegraph => ../plugins/plugin-codegraph

replace github.com/chonkpilot/chonkpilot-plugin-compress => ../plugins/plugin-compress

replace github.com/chonkpilot/chonkpilot-plugin-history => ../plugins/plugin-history

replace github.com/chonkpilot/chonkpilot-plugin-memory => ../plugins/plugin-memory

replace github.com/chonkpilot/chonkpilot-plugin-vfts => ../plugins/plugin-vfts

replace github.com/chonkpilot/chonkpilot-router => ../lib/router

replace github.com/chonkpilot/chonkpilot-task => ../lib/task

require github.com/chonkpilot/chonkpilot-assembly v0.0.0 // indirect

replace github.com/chonkpilot/chonkpilot-assembly => ../lib/assembly
