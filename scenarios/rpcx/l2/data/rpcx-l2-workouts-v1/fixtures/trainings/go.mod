module github.com/ThreeDotsLabs/wild-workouts-go-ddd-example/internal/trainings

go 1.25.0

require (
	example.com/rpcx v1.0.0
	github.com/ThreeDotsLabs/wild-workouts-go-ddd-example/internal/common v0.0.0-00010101000000-000000000000
	github.com/google/uuid v1.6.0
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.10.1
	github.com/stretchr/testify v1.12.1
)

require (
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/ThreeDotsLabs/wild-workouts-go-ddd-example/internal/common => ../common

replace example.com/rpcx => ../rpcx-v1

replace example.com/rpcx/v2 => ../rpcx-v2
