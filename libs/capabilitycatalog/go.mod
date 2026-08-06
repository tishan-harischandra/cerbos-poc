module github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog

go 1.25.11

require (
	github.com/tishan-harischandra/cerbos-poc/libs/cataloggen v0.0.0-00010101000000-000000000000
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/tishan-harischandra/cerbos-poc/libs/cataloggen => ../cataloggen
