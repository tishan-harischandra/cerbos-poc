module github.com/tishan-harischandra/cerbos-poc/tests/architecture

go 1.25

require github.com/tishan-harischandra/cerbos-poc/libs/cataloggen v0.0.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

replace github.com/tishan-harischandra/cerbos-poc/libs/cataloggen => ../../libs/cataloggen
