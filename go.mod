module github.com/UNO-SOFT/soap-proxy

go 1.15

require (
	aqwari.net/xml v0.0.0-20210331023308-d9421b293817
	github.com/UNO-SOFT/grpcer v0.8.2
	github.com/dgraph-io/badger/v3 v3.2103.1 // indirect
	github.com/go-kit/log v0.2.0 // indirect
	github.com/go-logr/logr v1.2.3
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/rogpeppe/retry v0.1.0
	github.com/tgulacsi/oracall v0.24.1
	golang.org/x/lint v0.0.0-20210508222113-6edffad5e616 // indirect
	golang.org/x/net v0.0.0-20220531201128-c960675eff93
	golang.org/x/oauth2 v0.0.0-20210514164344-f6687ab2804c // indirect
	golang.org/x/tools v0.1.10 // indirect
	golang.org/x/xerrors v0.0.0-20220517211312-f3a8303e98df // indirect
	google.golang.org/grpc v1.47.0
	google.golang.org/protobuf v1.28.0
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b // indirect
)

//replace github.com/UNO-SOFT/grpcer => ../grpcer
