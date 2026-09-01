module github.com/tishan-harischandra/cerbos-poc/apps/resource-service

go 1.25.11

require (
	github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore v0.0.0
	github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory v0.0.0-20260805202053-720924328c18
	github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry v0.0.0
	github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.10.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

require (
	github.com/tishan-harischandra/cerbos-poc/libs/canonicalid v0.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore => ../../libs/assignmentstore

replace github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry => ../../libs/tenantregistry

replace github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier => ../../libs/tokenverifier

replace github.com/tishan-harischandra/cerbos-poc/libs/canonicalid => ../../libs/canonicalid

replace github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory => ../../libs/idpdirectory
