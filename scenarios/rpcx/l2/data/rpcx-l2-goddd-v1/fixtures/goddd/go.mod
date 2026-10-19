module github.com/marcusolsson/goddd

go 1.23.0

require (
	example.com/rpcx v1.0.0
	github.com/go-chi/chi v3.3.3+incompatible
	github.com/go-kit/kit v0.7.0
	github.com/pborman/uuid v0.0.0-20180827223501-4c1ecd6722e8
)

require (
	github.com/VividCortex/gohistogram v1.0.0 // indirect
	github.com/go-logfmt/logfmt v0.3.0 // indirect
	github.com/go-stack/stack v1.8.0 // indirect
	github.com/kr/logfmt v0.0.0-20140226030751-b84e30acd515 // indirect
)

replace example.com/rpcx => ../rpcx-v1

replace example.com/rpcx/v2 => ../rpcx-v2
