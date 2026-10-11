module github.com/forgehubproject/fhr/handler-yaml

go 1.22.0

require (
	github.com/forgehubproject/fhr/packages/go v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/forgehubproject/fhr/packages/go => ../go
