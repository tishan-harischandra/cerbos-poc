module github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory

go 1.25

require (
	github.com/tishan-harischandra/cerbos-poc/libs/canonicalid v0.0.0
	github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier v0.0.0
)

replace github.com/tishan-harischandra/cerbos-poc/libs/canonicalid => ../canonicalid

replace github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier => ../tokenverifier
