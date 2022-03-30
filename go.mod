module github.com/UNO-SOFT/soap-proxy

go 1.15

require (
	aqwari.net/xml v0.0.0-20181013063537-841f47b2a098
	github.com/UNO-SOFT/grpcer v0.7.8
	github.com/hashicorp/go-retryablehttp v0.7.0
	github.com/tgulacsi/go v0.19.3
	github.com/tgulacsi/oracall v0.22.1
	golang.org/x/net v0.0.0-20210917221730-978cfadd31cf
	google.golang.org/grpc v1.40.0
	google.golang.org/protobuf v1.28.0
)

//replace github.com/UNO-SOFT/grpcer => ../grpcer
