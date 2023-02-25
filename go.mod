module github.com/UNO-SOFT/soap-proxy

go 1.15

require (
	aqwari.net/xml v0.0.0-20210331023308-d9421b293817
	github.com/UNO-SOFT/grpcer v0.8.4
	github.com/go-logr/logr v1.2.3
	github.com/klauspost/compress v1.15.15
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/rogpeppe/retry v0.1.0
	github.com/tgulacsi/oracall v0.24.1
	golang.org/x/net v0.7.0
	google.golang.org/grpc v1.53.0
	google.golang.org/protobuf v1.28.1
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
)

//replace github.com/UNO-SOFT/grpcer => ../grpcer
