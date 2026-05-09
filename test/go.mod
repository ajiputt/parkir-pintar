module github.com/ajiperdana/parkir-pintar/test

go 1.22

require (
	github.com/ajiperdana/parkir-pintar/pkg v0.0.0
	github.com/ajiperdana/parkir-pintar/proto v0.0.0
	github.com/stretchr/testify v1.9.0
	github.com/testcontainers/testcontainers-go v0.32.0
	github.com/testcontainers/testcontainers-go/modules/postgres v0.32.0
	github.com/testcontainers/testcontainers-go/modules/redis v0.32.0
)

replace (
	github.com/ajiperdana/parkir-pintar/pkg => ../pkg
	github.com/ajiperdana/parkir-pintar/proto => ../proto
)
