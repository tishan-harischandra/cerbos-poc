module github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore

go 1.25.11

require (
	github.com/jackc/pgx/v5 v5.10.0
	github.com/sijms/go-ora/v2 v2.9.0
	github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry v0.0.0
)

replace github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry => ../tenantregistry

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
