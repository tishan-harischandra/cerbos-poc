module github.com/tishan-harischandra/cerbos-poc/libs/capabilityeval

go 1.25.11

require github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog v0.0.0

require (
	github.com/tishan-harischandra/cerbos-poc/libs/cataloggen v0.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog => ../capabilitycatalog

replace github.com/tishan-harischandra/cerbos-poc/libs/cataloggen => ../cataloggen
