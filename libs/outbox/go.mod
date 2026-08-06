module github.com/tishan-harischandra/cerbos-poc/libs/outbox

go 1.25.11

require (
	github.com/segmentio/kafka-go v0.4.51
	github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore v0.0.0
)

require (
	github.com/klauspost/compress v1.15.9 // indirect
	github.com/pierrec/lz4/v4 v4.1.15 // indirect
)

replace github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore => ../assignmentstore
