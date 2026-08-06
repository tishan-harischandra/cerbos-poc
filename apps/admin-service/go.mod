module github.com/tishan-harischandra/cerbos-poc/apps/admin-service

go 1.25.11

require (
	github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore v0.0.0
	github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog v0.0.0
	github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory v0.0.0
	github.com/tishan-harischandra/cerbos-poc/libs/outbox v0.0.0
	github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier v0.0.0
)

require (
	github.com/tishan-harischandra/cerbos-poc/libs/canonicalid v0.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore => ../../libs/assignmentstore

replace github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog => ../../libs/capabilitycatalog

replace github.com/tishan-harischandra/cerbos-poc/libs/cataloggen => ../../libs/cataloggen

replace github.com/tishan-harischandra/cerbos-poc/libs/canonicalid => ../../libs/canonicalid

replace github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory => ../../libs/idpdirectory

replace github.com/tishan-harischandra/cerbos-poc/libs/outbox => ../../libs/outbox

replace github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier => ../../libs/tokenverifier
