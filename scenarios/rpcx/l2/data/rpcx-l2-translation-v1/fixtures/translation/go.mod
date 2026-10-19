module github.com/evrone/go-clean-template

go 1.26.0

require (
	example.com/rpcx v1.0.0
	github.com/stretchr/testify v1.12.1
	go.opentelemetry.io/otel v1.45.0
	go.opentelemetry.io/otel/trace v1.45.0
	go.uber.org/mock v0.6.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/metric v1.45.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)

replace example.com/rpcx => ../rpcx-v1

replace example.com/rpcx/v2 => ../rpcx-v2
