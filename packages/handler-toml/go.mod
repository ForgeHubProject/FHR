module github.com/forgehubproject/fhr/handler-toml

go 1.22.0

require (
	github.com/BurntSushi/toml v1.4.0
	github.com/forgehubproject/fhr/packages/go v0.0.0
)

replace github.com/forgehubproject/fhr/packages/go => ../go
