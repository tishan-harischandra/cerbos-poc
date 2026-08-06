module github.com/tishan-harischandra/cerbos-poc/apps/admin-service

go 1.25.11

require (
	github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore v0.0.0
	github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog v0.0.0
	github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory v0.0.0
	github.com/tishan-harischandra/cerbos-poc/libs/outbox v0.0.0-00010101000000-000000000000
	github.com/tishan-harischandra/cerbos-poc/libs/permissionevents v0.0.0
	github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.10.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.15.9 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.15 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/segmentio/kafka-go v0.4.51 // indirect
	github.com/tishan-harischandra/cerbos-poc/libs/canonicalid v0.0.0 // indirect
	github.com/tishan-harischandra/cerbos-poc/libs/cataloggen v0.0.0-00010101000000-000000000000 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore => ../../libs/assignmentstore

replace github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog => ../../libs/capabilitycatalog

replace github.com/tishan-harischandra/cerbos-poc/libs/cataloggen => ../../libs/cataloggen

replace github.com/tishan-harischandra/cerbos-poc/libs/canonicalid => ../../libs/canonicalid

replace github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory => ../../libs/idpdirectory

replace github.com/tishan-harischandra/cerbos-poc/libs/outbox => ../../libs/outbox

replace github.com/tishan-harischandra/cerbos-poc/libs/permissionevents => ../../libs/permissionevents

replace github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier => ../../libs/tokenverifier
